package httploc

import (
	timetools "github.com/mailgun/gotools-time"
	"github.com/mailgun/vulcan"
	. "github.com/mailgun/vulcan/endpoint"
	. "github.com/mailgun/vulcan/loadbalance"
	"github.com/mailgun/vulcan/loadbalance/roundrobin"
	. "github.com/mailgun/vulcan/middleware"
	"github.com/mailgun/vulcan/netutils"
	. "github.com/mailgun/vulcan/request"
	. "github.com/mailgun/vulcan/route"
	. "github.com/mailgun/vulcan/testutils"
	. "launchpad.net/gocheck"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type LocSuite struct {
	authHeaders http.Header
	tm          *timetools.FreezedTime
}

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&LocSuite{
	authHeaders: http.Header{
		"Authorization": []string{"Basic QWxhZGRpbjpvcGVuIHNlc2FtZQ=="},
	},
	tm: &timetools.FreezedTime{
		CurrentTime: time.Date(2012, 3, 4, 5, 6, 7, 0, time.UTC),
	},
})

func (s *LocSuite) newRoundRobin(endpoints ...string) LoadBalancer {
	rr, err := roundrobin.NewRoundRobinWithOptions(roundrobin.Options{TimeProvider: s.tm})
	if err != nil {
		panic(err)
	}
	for _, e := range endpoints {
		rr.AddEndpoint(MustParseUrl(e))
	}
	return rr
}

func (s *LocSuite) newProxyWithParams(
	l LoadBalancer,
	readTimeout time.Duration,
	dialTimeout time.Duration) (*HttpLocation, *httptest.Server) {

	location, err := NewLocationWithOptions("dummy", l, Options{
		TrustForwardHeader: true,
	})
	if err != nil {
		panic(err)
	}
	proxy, err := vulcan.NewProxy(&ConstRouter{
		Location: location,
	})
	if err != nil {
		panic(err)
	}
	return location, httptest.NewServer(proxy)
}

func (s *LocSuite) newProxy(l LoadBalancer) (*HttpLocation, *httptest.Server) {
	return s.newProxyWithParams(l, time.Duration(0), time.Duration(0))
}

// Success, make sure we've successfully proxied the response
func (s *LocSuite) TestSuccess(c *C) {
	server := NewTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hi, I'm endpoint"))
	})
	defer server.Close()

	_, proxy := s.newProxy(s.newRoundRobin(server.URL))
	defer proxy.Close()

	response, bodyBytes := Get(c, proxy.URL, s.authHeaders, "hello!")
	c.Assert(response.StatusCode, Equals, http.StatusOK)
	c.Assert(string(bodyBytes), Equals, "Hi, I'm endpoint")
}

// Make sure failover works
func (s *LocSuite) TestFailover(c *C) {
	server := NewTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hi, I'm endpoint"))
	})
	defer server.Close()

	_, proxy := s.newProxy(s.newRoundRobin("http://localhost:63999", server.URL))
	defer proxy.Close()

	response, bodyBytes := Get(c, proxy.URL, s.authHeaders, "hello!")
	c.Assert(response.StatusCode, Equals, http.StatusOK)
	c.Assert(string(bodyBytes), Equals, "Hi, I'm endpoint")
}

// Test scenario when middleware intercepts the request
func (s *LocSuite) TestMiddlewareInterceptsRequest(c *C) {
	server := NewTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hi, I'm endpoint"))
	})
	defer server.Close()

	location, proxy := s.newProxy(s.newRoundRobin(server.URL))
	defer proxy.Close()

	calls := make(map[string]int)

	auth := &MiddlewareWrapper{
		OnRequest: func(r Request) (*http.Response, error) {
			calls["authReq"] += 1
			return netutils.NewTextResponse(
				r.GetHttpRequest(),
				http.StatusForbidden,
				"Intercepted Request"), nil
		},
		OnResponse: func(r Request, a Attempt) {
			calls["authRe"] += 1
		},
	}

	cb := &MiddlewareWrapper{
		OnRequest: func(r Request) (*http.Response, error) {
			calls["cbReq"] += 1
			return nil, nil
		},
		OnResponse: func(r Request, a Attempt) {
			calls["cbRe"] += 1
		},
	}

	observer := &ObserverWrapper{
		OnRequest: func(r Request) {
			calls["oReq"] += 1
		},
		OnResponse: func(r Request, a Attempt) {
			calls["oRe"] += 1
		},
	}

	location.GetMiddlewareChain().Append("auth", auth)
	location.GetMiddlewareChain().Append("cb", cb)
	location.GetObserverChain().Append("ob", observer)

	response, bodyBytes := Get(c, proxy.URL, s.authHeaders, "hello!")
	c.Assert(response.StatusCode, Equals, http.StatusForbidden)
	c.Assert(string(bodyBytes), Equals, "Intercepted Request")

	// Auth middleware has been called on response as well
	c.Assert(calls["authReq"], Equals, 1)
	c.Assert(calls["authRe"], Equals, 1)

	// Callback has never got to a request, because it was intercepted
	c.Assert(calls["cbReq"], Equals, 0)
	c.Assert(calls["cbRe"], Equals, 0)

	// Observer was called regardless
	c.Assert(calls["oReq"], Equals, 1)
	c.Assert(calls["oRe"], Equals, 1)
}
