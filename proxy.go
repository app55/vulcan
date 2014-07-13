// This package contains the reverse proxy that implements http.HandlerFunc
package vulcan

import (
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"sync/atomic"

	log "github.com/mailgun/gotools-log"
	"github.com/mailgun/vulcan/errors"
	"github.com/mailgun/vulcan/netutils"
	"github.com/mailgun/vulcan/request"
	"github.com/mailgun/vulcan/route"
)

type Proxy struct {
	// Router selects a location for each request
	router route.Router
	// Options like ErrorFormatter
	options Options
	// Counter that is used to provide unique identifiers for requests
	lastRequestId int64
}

type Options struct {
	// Takes a status code and formats it into proxy response
	ErrorFormatter errors.Formatter
}

// Accepts requests, round trips it to the endpoint, and writes back the response.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := p.proxyRequest(w, r); err != nil {
		p.replyError(err, w, r)
	}
}

// Creates a proxy with a given router
func NewProxy(router route.Router) (*Proxy, error) {
	return NewProxyWithOptions(router, Options{})
}

// Creates reverse proxy that acts like http request handler
func NewProxyWithOptions(router route.Router, o Options) (*Proxy, error) {
	o, err := validateOptions(o)
	if err != nil {
		return nil, err
	}

	p := &Proxy{
		options: o,
		router:  router,
	}
	return p, nil
}

func (p *Proxy) GetRouter() route.Router {
	return p.router
}

// Round trips the request to the selected location and writes back the response
func (p *Proxy) proxyRequest(w http.ResponseWriter, r *http.Request) error {

	// Create a unique request with sequential ids that will be passed to all interfaces.
	req := request.NewBaseRequest(r, atomic.AddInt64(&p.lastRequestId, 1), nil)
	location, err := p.router.Route(req)
	if err != nil {
		return err
	}

	// Router could not find a matching location, we can do nothing else.
	if location == nil {
		log.Errorf("%s failed to route", req)
		return errors.FromStatus(http.StatusBadGateway)
	}

	// The next step is to read the body based on the location settings
	var reader request.BodyReader
	if customReader, ok := location.(request.BodyReader); ok {
		reader = customReader
	} else {
		reader = &request.BaseBodyReader{}
	}

	// Record the request body so we can replay it on errors.
	body, err := reader.ReadBody(r.Body)
	if err != nil || body == nil {
		log.Errorf("Request read error %s", err)
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return errors.FromStatus(http.StatusRequestTimeout)
		} else if _, ok := err.(*netutils.MaxSizeReachedError); ok {
			return errors.FromStatus(http.StatusRequestEntityTooLarge)
		} else {
			return errors.FromStatus(http.StatusBadRequest)
		}
	}
	defer body.Close()
	r.Body = body
	req.Body = body

	response, err := location.RoundTrip(req)
	if response != nil {
		netutils.CopyHeaders(w.Header(), response.Header)
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
		defer response.Body.Close()
		return nil
	} else {
		return err
	}
}

// Helper function to reply with http errors
func (p *Proxy) replyError(err error, w http.ResponseWriter, req *http.Request) {
	// Discard the request body, so that clients can actually receive the response
	// otherwise they can only see lost connection
	// TODO: actually check this
	proxyError, ok := err.(errors.ProxyError)
	if !ok {
		proxyError = errors.FromStatus(http.StatusBadGateway)
	}

	io.Copy(ioutil.Discard, req.Body)
	statusCode, body, contentType := p.options.ErrorFormatter.Format(proxyError)
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(statusCode)
	w.Write(body)
}

func validateOptions(o Options) (Options, error) {
	if o.ErrorFormatter == nil {
		o.ErrorFormatter = &errors.JsonFormatter{}
	}
	return o, nil
}
