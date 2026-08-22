package api

import (
	"net/url"
	"strings"
)

// req is the internal request description used by Do.
type req struct {
	method string
	path   string
	body   any
	query  string
	reason string
	bucket string

	// multipart upload (stickers etc). When fileBytes != nil, body is ignored.
	formFields map[string]string
	fileField  string
	fileName   string
	fileType   string
	fileBytes  []byte

	noAuth bool // webhook execute routes don't need the token header
}

// ReqOpt mutates a req.
type ReqOpt func(*req)

func newReq(method, path string, body any, opts ...ReqOpt) *req {
	r := &req{method: method, path: path, body: body}
	for _, o := range opts {
		o(r)
	}
	return r
}

// WithReason sets the X-Audit-Log-Reason header.
func WithReason(s string) ReqOpt { return func(r *req) { r.reason = s } }

// WithQuery appends a query string (already URL-encoded).
func WithQuery(kv map[string]string) ReqOpt {
	return func(r *req) {
		v := url.Values{}
		for k, val := range kv {
			v.Set(k, val)
		}
		r.query = v.Encode()
	}
}

// WithBucket sets the bucket id to pre-check before issuing the request.
func WithBucket(id string) ReqOpt { return func(r *req) { r.bucket = id } }

// WithFields sets multipart form fields (used with WithFile).
func WithFields(kv map[string]string) ReqOpt {
	return func(r *req) {
		if r.formFields == nil {
			r.formFields = make(map[string]string, len(kv))
		}
		for k, v := range kv {
			r.formFields[k] = v
		}
	}
}

// WithFile attaches a multipart file part.
func WithFile(field, name, contentType string, data []byte) ReqOpt {
	return func(r *req) {
		r.fileField = field
		r.fileName = name
		r.fileType = contentType
		r.fileBytes = data
	}
}

// NoAuth skips the Authorization header (webhook execute by id+token).
func NoAuth() ReqOpt { return func(r *req) { r.noAuth = true } }

// Path builds a snowflake-interpolated path.
func Path(template string, ids ...any) string {
	// Replace each %s placeholder.
	for _, id := range ids {
		template = strings.Replace(template, "%s", idToStr(id), 1)
	}
	return template
}

func idToStr(id any) string {
	switch v := id.(type) {
	case string:
		return v
	case int64:
		return formatInt(v)
	case int:
		return formatInt(int64(v))
	case uint64:
		return formatUint(v)
	default:
		return ""
	}
}

func formatInt(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func formatUint(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
