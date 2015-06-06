package pcs

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/google/go-querystring/query"
)

const (
	defaultBaseURL  = "https://pcs.baidu.com/rest/2.0/pcs"
	uploadBaseURL   = "https://c.pcs.baidu.com/rest/2.0/pcs"
	downloadBaseURL = "https://d.pcs.baidu.com/rest/2.0/pcs"

	libraryVersion = "0.1"
	userAgent      = "go-baidupcs/" + libraryVersion

	minRapidUploadFile = 256 * 1024

	defaultIdleConns = 128
)

var (
	ErrInvalidArgument  = errors.New("baidu-pcs: invalid argument")
	ErrMinRapidFileSize = errors.New("baidu-pcs: rapid upload file size must > 256KB")
	ErrIncompleteFile   = errors.New("baidu-pcs: could not read the whole file")
)

// TODO: 参考go-github 重构。

// TODO 检查文件
// 上传文件路径（含上传的文件名称）。
// 注意：
// 路径长度限制为1000
// 路径中不能包含以下字符：\\ ? | " > < : *
// 文件名或路径名开头结尾不能是“.”或空白字符，空白字符包括: \r, \n, \t, 空格, \0, \x0B

type Client struct {
	BaseURL     *url.URL
	UploadURL   *url.URL
	DownloadURL *url.URL

	UserAgent   string
	AccessToken string
	client      *http.Client
}

func NewClient(accessToken string) *Client {
	client := new(Client)

	baseURL, _ := url.Parse(defaultBaseURL)
	uploadURL, _ := url.Parse(uploadBaseURL)
	downloadURL, _ := url.Parse(downloadBaseURL)

	client.BaseURL = baseURL
	client.UploadURL = uploadURL
	client.DownloadURL = downloadURL

	client.UserAgent = userAgent
	client.AccessToken = accessToken
	client.client = NewHttpClient()

	return client
}

func NewHttpClient() *http.Client {
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
	return &http.Client{Transport: tr}
}

func (c *Client) Get(url string, v interface{}) (*http.Response, error) {
	req, err := c.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req, v)
}

func (c *Client) Post(url string, contentType string, body io.Reader, v interface{}) (*http.Response, error) {
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.Do(req, v)
}

func (c *Client) PostForm(url string, data url.Values, v interface{}) (*http.Response, error) {
	return c.Post(url, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()), v)
}

func (c *Client) addOptions(s string, method string, opt interface{}) (string, error) {
	v := reflect.ValueOf(opt)
	if v.Kind() == reflect.Ptr && v.IsNil() {
		return s, nil
	}

	u, err := url.Parse(s)
	if err != nil {
		return s, err
	}

	qs, err := query.Values(opt)
	if err != nil {
		return s, err
	}

	qs.Set("access_token", c.AccessToken)
	qs.Set("method", method)

	u.RawQuery = qs.Encode()
	return u.String(), nil
}

// NewRequest creates an API request. A relative URL can be provided in urlStr,
// in which case it is resolved relative to the BaseURL of the Client.
// Relative URLs should always be specified without a preceding slash.  If
// specified, the value pointed to by body is JSON encoded and included as the
// request body.
func (c *Client) NewRequest(method, urlStr string, body io.Reader) (*http.Request, error) {
	rel, err := url.Parse(urlStr)
	if err != nil {
		return nil, err
	}

	u := c.BaseURL.ResolveReference(rel)

	req, err := http.NewRequest(method, u.String(), body)
	if err != nil {
		return nil, err
	}

	if c.UserAgent != "" {
		req.Header.Add("User-Agent", c.UserAgent)
	}
	return req, nil
}

func (c *Client) NewUploadRequest(method, urlStr string, body io.Reader) (*http.Request, error) {
	rel, err := url.Parse(urlStr)
	if err != nil {
		return nil, err
	}

	u := c.UploadURL.ResolveReference(rel)
	req, err := http.NewRequest(method, u.String(), body)
	if err != nil {
		return nil, err
	}

	if c.UserAgent != "" {
		req.Header.Add("User-Agent", c.UserAgent)
	}
	return req, nil

}

func (c *Client) NewDownloadRequest(method, urlStr string, body io.Reader) (*http.Request, error) {
	rel, err := url.Parse(urlStr)
	if err != nil {
		return nil, err
	}

	u := c.DownloadURL.ResolveReference(rel)
	req, err := http.NewRequest(method, u.String(), body)
	if err != nil {
		return nil, err
	}

	if c.UserAgent != "" {
		req.Header.Add("User-Agent", c.UserAgent)
	}
	return req, nil

}

func (c *Client) Do(req *http.Request, v interface{}) (*http.Response, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	err = CheckResponse(resp)
	if err != nil {
		// even though there was an error, we still return the response
		// in case the caller wants to inspect it further
		return resp, err
	}

	if v != nil {
		if w, ok := v.(io.Writer); ok {
			io.Copy(w, resp.Body)
		} else {
			err = json.NewDecoder(resp.Body).Decode(v)
		}
	}

	return resp, err
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

func CheckResponse(r *http.Response) error {
	if c := r.StatusCode; 200 <= c && c <= 299 {
		return nil
	}
	errorResponse := &ErrorResponse{Response: r}
	data, err := ioutil.ReadAll(r.Body)
	if err == nil && data != nil {
		json.Unmarshal(data, errorResponse)
	}
	return errorResponse
}
