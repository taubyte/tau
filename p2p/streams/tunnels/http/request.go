package httptun

import (
	"io"
	"net/http"
	"net/url"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/p2p/streams/packer"
)

type requestPayload struct {
	Method string `cbor:"1,keyasint"`
	URL    string `cbor:"2,keyasint"`

	Proto      string `cbor:"3,keyasint"`
	ProtoMajor int    `cbor:"4,keyasint"`
	ProtoMinor int    `cbor:"5,keyasint"`

	Headers       http.Header `cbor:"6,keyasint"`
	ContentLength int64       `cbor:"7,keyasint"`

	TransferEncoding []string `cbor:"8,keyasint"`

	Close bool `cbor:"9,keyasint"`

	Host string `cbor:"10,keyasint"`

	Form url.Values `cbor:"11,keyasint"`

	PostForm url.Values `cbor:"12,keyasint"`

	Trailer http.Header `cbor:"13,keyasint"`

	RemoteAddr string `cbor:"14,keyasint"`

	RequestURI string `cbor:"15,keyasint"`
}

func requestToPayload(req *http.Request) *requestPayload {
	return &requestPayload{
		Method:           req.Method,
		URL:              req.URL.String(),
		Proto:            req.Proto,
		ProtoMajor:       req.ProtoMajor,
		ProtoMinor:       req.ProtoMinor,
		Headers:          req.Header,
		ContentLength:    req.ContentLength,
		TransferEncoding: req.TransferEncoding,
		Close:            req.Close,
		Host:             req.Host,
		Form:             req.Form,
		PostForm:         req.PostForm,
		Trailer:          req.Trailer,
		RemoteAddr:       req.RemoteAddr,
		RequestURI:       req.RequestURI,
	}
}

// make sure body passed does not close connection
func payloadToRequest(payload *requestPayload, body io.Reader) (*http.Request, error) {
	// Create the request URL.
	parsedURL, err := url.Parse(payload.URL)
	if err != nil {
		return nil, err
	}

	pack := packer.New(Magic, Version)

	// Create a new request.
	req, err := http.NewRequest(payload.Method, parsedURL.String(), newBodyReader(pack, BodyOp, body))
	if err != nil {
		return nil, err
	}

	// Populate the request fields from the payload.
	req.Proto = payload.Proto
	req.ProtoMajor = payload.ProtoMajor
	req.ProtoMinor = payload.ProtoMinor
	req.Header = payload.Headers
	req.ContentLength = payload.ContentLength
	req.TransferEncoding = payload.TransferEncoding
	req.Close = payload.Close
	req.Host = payload.Host
	req.Form = payload.Form
	req.PostForm = payload.PostForm
	req.Trailer = payload.Trailer
	req.RemoteAddr = payload.RemoteAddr
	req.RequestURI = payload.RequestURI

	return req, nil
}

func (r *requestPayload) Encode() ([]byte, error) {
	return cbor.Marshal(r)
}

func (r *requestPayload) Decode(b []byte) error {
	return cbor.Unmarshal(b, r)
}
