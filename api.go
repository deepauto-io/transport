package transport

import (
	"compress/gzip"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"github.com/deepauto-io/errors"
	"github.com/deepauto-io/log"
	"io"
	"net/http"
)

// PlatformErrorCodeHeader shows the error code of platform error.
const PlatformErrorCodeHeader = "X-Platform-Error-Code"

// API provides a consolidated means for handling API interface concerns.
// Concerns such as decoding/encoding request and response bodies as well
// as adding headers for content type and content encoding.
type API struct {
	logger log.Logger

	prettyJSON bool
	encodeGZIP bool

	unmarshalErrFn func(encoding string, err error) error
	okErrFn        func(err error) error
	errFn          func(ctx context.Context, err error) (interface{}, int, error)
}

// APIOptFn is a functional option for setting fields on the API type.
type APIOptFn func(*API)

// WithLog sets the logger.
func WithLog(logger log.Logger) APIOptFn {
	return func(api *API) {
		api.logger = logger
	}
}

// WithErrFn sets to err handling func for issues when writing to the response body.
func WithErrFn(fn func(ctx context.Context, err error) (interface{}, int, error)) APIOptFn {
	return func(api *API) {
		api.errFn = fn
	}
}

// WithOKErrFn is an error handler for failing validation for request bodies.
func WithOKErrFn(fn func(err error) error) APIOptFn {
	return func(api *API) {
		api.okErrFn = fn
	}
}

// WithPrettyJSON sets the json encoder to marshal indent or not.
func WithPrettyJSON(b bool) APIOptFn {
	return func(api *API) {
		api.prettyJSON = b
	}
}

// WithEncodeGZIP sets the encoder to gzip contents.
func WithEncodeGZIP() APIOptFn {
	return func(api *API) {
		api.encodeGZIP = true
	}
}

// WithUnmarshalErrFn sets the error handler for errors that occur when unmarshalling
// the request body.
func WithUnmarshalErrFn(fn func(encoding string, err error) error) APIOptFn {
	return func(api *API) {
		api.unmarshalErrFn = fn
	}
}

// NewAPI creates a new API type.
func NewAPI(opts ...APIOptFn) *API {
	api := API{
		prettyJSON: true,
		unmarshalErrFn: func(encoding string, err error) error {
			return &errors.Error{
				Code: errors.EInvalid,
				Msg:  fmt.Sprintf("failed to unmarshal %s: %s", encoding, err),
			}
		},
		errFn: func(ctx context.Context, err error) (interface{}, int, error) {
			msg := err.Error()
			if msg == "" {
				msg = "an internal error has occurred"
			}
			code := errors.ErrorCode(err)
			return ErrBody{
				Code: code,
				Msg:  msg,
			}, ErrorCodeToStatusCode(ctx, code), nil
		},
	}
	for _, o := range opts {
		o(&api)
	}
	return &api
}

// DecodeJSON decodes reader with json.
func (a *API) DecodeJSON(r io.Reader, v interface{}) error {
	return a.decode("json", json.NewDecoder(r), v)
}

// DecodeGob decodes reader with gob.
func (a *API) DecodeGob(r io.Reader, v interface{}) error {
	return a.decode("gob", gob.NewDecoder(r), v)
}

type (
	decoder interface {
		Decode(interface{}) error
	}

	oker interface {
		OK() error
	}
)

func (a *API) decode(encoding string, dec decoder, v interface{}) error {
	if err := dec.Decode(v); err != nil {
		if a != nil && a.unmarshalErrFn != nil {
			return a.unmarshalErrFn(encoding, err)
		}
		return err
	}

	if vv, ok := v.(oker); ok {
		err := vv.OK()
		if a != nil && a.okErrFn != nil {
			return a.okErrFn(err)
		}
		return err
	}

	return nil
}

// Respond writes to the response writer, handling all errors in writing.
func (a *API) Respond(w http.ResponseWriter, r *http.Request, status int, v interface{}) {
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}

	var writer io.WriteCloser = noopCloser{Writer: w}
	// we'll double close to make sure its always closed even
	//on issues before to write
	defer writer.Close()

	if a != nil && a.encodeGZIP {
		w.Header().Set("Content-Encoding", "gzip")
		writer = gzip.NewWriter(w)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// this marshal block is to catch failures before they hit the http writer.
	// default behavior for http.ResponseWriter is when body is written and no
	// status is set, it writes a 200. Or if a status is set before encoding
	// and an error occurs, there is no means to write a proper status code
	// (i.e. 500) when that is to occur. This brings that step out before
	// and then writes the data and sets the status code after marshaling
	// succeeds.
	var (
		b   []byte
		err error
	)
	if a == nil || a.prettyJSON {
		b, err = json.MarshalIndent(v, "", "\t")
	} else {
		b, err = json.Marshal(v)
	}
	if err != nil {
		a.Err(w, r, err)
		return
	}

	a.write(w, writer, status, b)
}

// Write allows the user to write raw bytes to the response writer. This
// operation does not have a fail case, all failures here will be logged.
func (a *API) Write(w http.ResponseWriter, status int, b []byte) {
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}

	var writer io.WriteCloser = noopCloser{Writer: w}
	// we'll double close to make sure its always closed even
	//on issues before to write
	defer writer.Close()

	if a != nil && a.encodeGZIP {
		w.Header().Set("Content-Encoding", "gzip")
		writer = gzip.NewWriter(w)
	}

	a.write(w, writer, status, b)
}

func (a *API) write(w http.ResponseWriter, wc io.WriteCloser, status int, b []byte) {
	w.WriteHeader(status)
	if _, err := wc.Write(b); err != nil {
		a.logger.
			WithField("api", "write").
			Error("failed to write to response writer: ", err)
	}

	if err := wc.Close(); err != nil {
		a.logger.
			WithField("api", "write").
			Error("failed to close response writer", err)
	}
}

// Err is used for writing an error to the response.
func (a *API) Err(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}

	v, status, err := a.errFn(r.Context(), err)
	if err != nil {
		a.logger.Error("failed to write err to response writer", err)
		a.Respond(w, r, http.StatusInternalServerError, ErrBody{
			Code: "internal error",
			Msg:  "an unexpected error occurred",
		})
		return
	}

	if eb, ok := v.(ErrBody); ok {
		w.Header().Set(PlatformErrorCodeHeader, eb.Code)
	}
	a.Respond(w, r, status, v)
}

type noopCloser struct {
	io.Writer
}

func (n noopCloser) Close() error {
	return nil
}

// ErrBody is an err response body.
type ErrBody struct {
	Code string `json:"code"`
	Msg  string `json:"message"`
}
