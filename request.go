package wine

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"github.com/gopub/gox"
	"github.com/gopub/wine/mime"
	"github.com/pkg/errors"
)

const (
	ContentType = "Content-Type"
)

// Request is a wrapper of http.Request, aims to provide more convenient interface
type Request struct {
	request     *http.Request
	params      gox.M
	body        []byte
	contentType string
}

func (r *Request) Request() *http.Request {
	return r.request
}

func (r *Request) Params() gox.M {
	return r.params
}

func (r *Request) Body() []byte {
	return r.body
}

func (r *Request) ContentType() string {
	return r.contentType
}

func NewRequest(r *http.Request, parser ParamsParser) (*Request, error) {
	if parser == nil {
		parser = NewDefaultParamsParser(nil, 8*gox.MB)
	}

	params, body, err := parser.Parse(r)
	if err != nil {
		return nil, err
	}
	return &Request{
		request:     r,
		params:      params,
		body:        body,
		contentType: GetContentType(r.Header),
	}, nil
}

type ParamsParser interface {
	Parse(req *http.Request) (gox.M, []byte, error)
}

type DefaultParamsParser struct {
	headerParamNames *gox.StringSet
	maxMemory        gox.ByteUnit
}

func NewDefaultParamsParser(headerParamNames []string, maxMemory gox.ByteUnit) *DefaultParamsParser {
	p := &DefaultParamsParser{
		headerParamNames: gox.NewStringSet(1),
		maxMemory:        maxMemory,
	}
	for _, n := range headerParamNames {
		p.headerParamNames.Add(n)
	}
	if p.maxMemory < gox.MB {
		p.maxMemory = gox.MB
	}
	return p
}

func (p *DefaultParamsParser) Parse(req *http.Request) (gox.M, []byte, error) {
	params := gox.M{}
	params.AddMap(p.parseCookie(req))
	params.AddMap(p.parseHeader(req))
	params.AddMap(p.parseURLValues(req.URL.Query()))
	bp, body, err := p.parseBody(req)
	if err != nil {
		return params, body, errors.Wrap(err, "parse body failed")
	}
	params.AddMap(bp)
	return params, body, nil
}

func (p *DefaultParamsParser) parseCookie(req *http.Request) gox.M {
	params := gox.M{}
	for _, cookie := range req.Cookies() {
		params[cookie.Name] = cookie.Value
	}
	return params
}

func (p *DefaultParamsParser) parseHeader(req *http.Request) gox.M {
	params := gox.M{}
	for k, v := range req.Header {
		if strings.HasPrefix(k, "x-") || strings.HasPrefix(k, "X-") || p.headerParamNames.Contains(k) {
			params[strings.ToLower(k[2:])] = v
		}
	}
	return params
}

func (p *DefaultParamsParser) parseURLValues(values url.Values) gox.M {
	m := gox.M{}
	for k, v := range values {
		i := strings.Index(k, "[]")
		if i >= 0 && i == len(k)-2 {
			k = k[0 : len(k)-2]
			if len(v) == 1 {
				v = strings.Split(v[0], ",")
			}
		}
		k = strings.ToLower(k)
		if len(v) > 1 || i >= 0 {
			m[k] = v
		} else if len(v) == 1 {
			m[k] = v[0]
		}
	}

	return m
}

func (p *DefaultParamsParser) parseBody(req *http.Request) (gox.M, []byte, error) {
	typ := GetContentType(req.Header)
	params := gox.M{}
	switch typ {
	case mime.HTML, mime.Plain:
		body, err := ioutil.ReadAll(req.Body)
		if err != nil {
			return params, nil, errors.Wrap(err, "read html or plain body failed")
		}
		return params, body, nil
	case mime.JSON:
		body, err := ioutil.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return params, nil, errors.Wrap(err, "read json body failed")
		}
		if len(body) == 0 {
			return params, nil, nil
		}
		decoder := json.NewDecoder(bytes.NewBuffer(body))
		decoder.UseNumber()
		err = decoder.Decode(&params)
		return params, body, errors.Wrapf(err, "decoder json failed")
	case mime.FormURLEncoded:
		body, err := req.GetBody()
		if err != nil {
			return params, nil, errors.Wrap(err, "get body failed")
		}
		bodyData, err := ioutil.ReadAll(body)
		body.Close()
		if err != nil {
			return params, nil, errors.Wrap(err, "read form body failed")
		}
		if err = req.ParseForm(); err != nil {
			return params, bodyData, errors.Wrap(err, "parse form failed")
		}
		return p.parseURLValues(req.Form), bodyData, nil
	case mime.FormData:
		err := req.ParseMultipartForm(int64(p.maxMemory))
		if err != nil {
			return nil, nil, errors.Wrap(err, "parse multipart form failed")
		}

		if req.MultipartForm != nil && req.MultipartForm.File != nil {
			return p.parseURLValues(req.MultipartForm.Value), nil, nil
		}
		return params, nil, nil
	default:
		if len(typ) != 0 {
			logger.Warnf("Ignored content type=%s", typ)
		}
		return params, nil, nil
	}
}

func GetContentType(h http.Header) string {
	t := h.Get(ContentType)
	for i, ch := range t {
		if ch == ' ' || ch == ';' {
			t = t[:i]
			break
		}
	}
	return t
}
