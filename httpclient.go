package pcs

import (
	"crypto/tls"
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

	if res.StatusCode != http.StatusOK {
		// return error.

	}

	// marshal error.

	return res, body, nil
}
