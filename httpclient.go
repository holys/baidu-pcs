package pcs

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultIdleConns = 160
)

type HttpClient struct {
	client *http.Client
}

func NewHttpClient() *HttpClient {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		Dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).Dial,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConnsPerHost: defaultIdleConns,
	}
	client := new(HttpClient)
	client.client = &http.Client{Transport: tr}
	return client
}

func (c *HttpClient) Get(url string) (*http.Response, []byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, nil, err
	}
	return c.Do(req)
}

func (c *HttpClient) Post(url string, contentType string, body io.Reader) (*http.Response, []byte, error) {
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.Do(req)
}

func (c *HttpClient) PostForm(url string, data url.Values) (*http.Response, []byte, error) {
	return c.Post(url, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
}

func (c *HttpClient) Do(req *http.Request) (*http.Response, []byte, error) {
	res, err := c.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, nil, err
	}

	err = CheckResponse(body, res)
	if err != nil {
		// even though there was an error, we still return the response
		// in case the caller wants to inspect it further
		return res, body, err
	}

	return res, body, nil
}

type ErrorResponse struct {
	Response *http.Response // HTTP response that caused this error
	Message  string         `json:"error_msg"`  // error message
	Code     int            `json:"error_code"` // error code
}

func (r *ErrorResponse) Error() string {
	return fmt.Sprintf("[%v] - %v - %d - %v - %d",
		r.Response.Request.Method, r.Response.Request.URL,
		r.Response.StatusCode, r.Message, r.Code)
}

func CheckResponse(body []byte, r *http.Response) error {
	// FIXME: what if 3XX?
	if c := r.StatusCode; 200 <= c && c <= 299 {
		return nil
	}
	errorResponse := &ErrorResponse{Response: r}
	if body != nil {
		json.Unmarshal(body, errorResponse)
	}
	return errorResponse
}
