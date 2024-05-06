/*
Copyright 2022 The deepauto-io LLC.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package transport

import (
	"bytes"
	"github.com/deepauto-io/log"
	ua "github.com/mileusna/useragent"
	"io"
	"net/http"
	"time"
)

// Middleware constructor.
type Middleware func(http.Handler) http.Handler

func SetCORS(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			// Access-Control-Allow-Origin must be present in every response
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		if r.Method == http.MethodOptions {
			// allow and stop processing in pre-flight requests
			w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE, PATCH")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization, User-Agent")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

// SkipOptions Preflight CORS requests from the browser will send an options request,
// so we need to make sure we satisfy them
func SkipOptions(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		// Preflight CORS requests from the browser will send an options request,
		// so we need to make sure we satisfy them
		if origin := r.Header.Get("Origin"); origin == "" && r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

// UserAgent gets the user agent for the HTTP request.
func UserAgent(r *http.Request) string {
	header := r.Header.Get("User-Agent")
	if header == "" {
		return "unknown"
	}
	return ua.Parse(header).Name
}

type bodyEchoer struct {
	rc    io.ReadCloser
	teedR io.Reader
}

func (b *bodyEchoer) Read(p []byte) (int, error) {
	return b.teedR.Read(p)
}

func (b *bodyEchoer) Close() error {
	return b.rc.Close()
}

// LoggingMW middleware for logging inflight http requests.
func LoggingMW(logger log.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			srw := NewStatusResponseWriter(w)
			var buf bytes.Buffer
			r.Body = &bodyEchoer{
				rc:    r.Body,
				teedR: io.TeeReader(r.Body, &buf),
			}

			defer func(start time.Time) {
				errReferenceField := ""
				if errReference := w.Header().Get(PlatformErrorCodeHeader); errReference != "" {
					errReferenceField = errReference
				}

				ip := r.Header.Get("X-Forwarded-For")
				if ip == "" {
					ip = r.RemoteAddr
				}

				logger.WithField("method", r.Method).
					WithField("host", r.Host).
					WithField("path", r.URL.Path).
					WithField("query", r.URL.Query().Encode()).
					WithField("proto", r.Proto).
					WithField("status_code", srw.Code()).
					WithField("response_size", srw.ResponseBytes()).
					WithField("content_length", r.ContentLength).
					WithField("referrer", r.Referer()).
					WithField("remote", ip).
					WithField("user_agent", UserAgent(r)).
					WithField("took", time.Since(start)).
					WithField("errReference", errReferenceField).
					Info("request")
			}(time.Now())
			next.ServeHTTP(srw, r)
		}
		return http.HandlerFunc(fn)
	}
}
