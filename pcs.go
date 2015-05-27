package pcs

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultBaseURL  = "https://pcs.baidu.com/rest/2.0/pcs"
	uploadBaseURL   = "https://c.pcs.baidu.com/rest/2.0/pcs"
	downloadBaseURL = "https://d.pcs.baidu.com/rest/2.0/pcs"

	minRapidUploadFile = 256 * 1024
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
	baseURL     *url.URL
	uploadURL   *url.URL
	downloadURL *url.URL

	client *HttpClient
	query  url.Values
}

func NewClient(accessToken string) *Client {
	client := new(Client)

	baseURL, _ := url.Parse(defaultBaseURL)
	uploadURL, _ := url.Parse(uploadBaseURL)
	downloadURL, _ := url.Parse(downloadBaseURL)

	client.baseURL = baseURL
	client.uploadURL = uploadURL
	client.downloadURL = downloadURL
	client.client = NewHttpClient()
	client.query = url.Values{}
	client.query["access_token"] = accessToken

	return client
}

type Quota struct {
	Quota uint64 `json:"quota"`
	Used  uint64 `json:"used"`
}

// 获取当前用户空间配额信息
func (c *Client) GetQuota() (*Quota, error) {
	c.baseURL.Path = filepath.Join(c.baseURL.Path, "quota")
	c.query.Set("method", "info")
	c.baseURL.RawQuery = c.query.Encode()

	_, body, err := c.client.Get(c.baseURL.String())
	if err != nil {
		return nil, err
	}

	q := new(Quota)
	err = json.Unmarshal(body, q)
	if err != nil {
		return nil, err
	}

	return q, nil
}

type File struct {
	Path  string `json:"path"`  // 文件的绝对路径
	Size  uint64 `json:"size"`  // 文件大小（以字节为单位）
	Ctime uint64 `json:"ctime"` // 文件创建时间
	Mtime uint64 `json:"mtime"` // 文件修改时间
	Md5   string `json:"md5"`   // 文件的md5签名
	FsId  uint64 `json:"fs_id"` // 文件在PCS的临时唯一标识ID
	IsDir uint   `json:"isdir"` // 是否是目录的标识符: “0”为文件, “1”为目录
}

// path: 待上传文件的相对路径
func (c *Client) upload(path string) (io.Reader, string, error) {
	// code adapted from http://matt.aimonetti.net/posts/2013/07/01/golang-multipart-file-upload-example/
	fullpath, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}

	file, err := os.Open(fullpath)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return nil, "", err
	}

	bytes, err = io.Copy(part, file)
	if err != nil {
		return nil, "", err
	}

	contentType := writer.FormDataContentType()
	writer.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, "", err
	}
	if bytes != stat.Size() {
		return nil, "", ErrIncompleteFile
	}

	return body, contentType, nil
}

func getOnDup(overwrite bool) string {
	var ondup string
	if overwrite {
		ondup = "overwrite"
	} else {
		ondup = "newcopy"
	}
	return ondup
}

// 上传单个文件
// srcPath: 上传文件的源路径
// targetPath: 上传文件的目标保存路径
func (c *Client) Upload(srcPath, targetPath string, overwrite bool) (*File, error) {
	c.uploadURL.Path = filepath.Join(c.uploadURL.Path, "file")

	body, contentType, err := c.upload(srcPath)
	if err != nil {
		return nil, err
	}

	c.query.Set("method", "upload")
	c.query.Set("path", targetPath)
	c.query.Set("ondup", getOnDup(overwrite))
	c.uploadURL.RawQuery = c.query.Encode()

	_, data, err = c.client.Post(c.uploadURL.String(), contentType, body)
	if err != nil {
		return nil, err
	}

	f := new(File)
	err = json.Unmarshal(data, f)
	if err != nil {
		return nil, err
	}

	return f, nil
}

// 分片上传—文件分片及上传
func (c *Client) BlockUpload(srcPath string) (*File, error) {
	c.uploadURL.Path = filepath.Join(c.uploadURL.Path, "file")
	body, contentType, err := c.upload(srcPath)
	if err != nil {
		return nil, err
	}

	c.query.Set("method", "upload")
	c.query.Set("type", "tmpfile")
	c.uploadURL.RawQuery = c.query.Encode()

	_, data, err = c.client.Post(c.uploadURL.String(), contentType, body)
	if err != nil {
		return nil, err
	}

	f := new(File)
	err = json.Unmarshal(data, f)
	if err != nil {
		return nil, err
	}

	return f, nil
}

// 分片上传—合并分片文件
// 与分片文件上传的upload方法配合使用，可实现超大文件（>2G）上传，同时也可用于断点续传的场景。
func (c *Client) CreateSuperFile(targetPath string, md5 []string, overwrite bool) (*File, error) {
	if len(md5) < 2 || len(md5) > 1024 {
		return nil, ErrInvalidArgument
	}

	c.baseURL.Path = filepath.Join(c.baseURL.Path, "file")

	tmp := make(map[string][]string)
	tmp["blocklist"] = md5
	param, err := json.Marshal(tmp)
	if err != nil {
		return nil, err
	}

	c.query.Set("method", "createsuperfile")
	c.query.Set("path", targetPath)
	c.query.Set("ondup", getOnDup(overwrite))
	c.baseURL.RawQuery = c.query.Encode()

	d := url.Values{}
	d.Set("param", string(param))

	_, data, err := c.client.PostForm(c.baseURL.String(), strings.NewReader(d.Encode()))
	if err != nil {
		return nil, err
	}

	f := new(File)
	err = json.Unmarshal(data, f)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// 下载单个文件
// path: 下载文件路径，以/开头的绝对路径。
func (c *Client) Download(path string) error {
	c.downloadURL.Path = filepath.Join(c.downloadURL.Path, "file")

	c.query.Set("method", "download")
	c.query.Set("path", path)
	c.downloadURL.RawQuery = c.query.Encode()

	_, _, err = c.client.Get(c.downloadURL.String())
	return err
}

// 创建目录
func (c *Client) Mkdir(path string) (*File, error) {
	c.baseURL.Path = filepath.Join(c.baseURL.Path, "file")

	c.query.Set("method", "mkdir")
	c.query.Set("path", path)
	c.baseURL.RawQuery = c.query.Encode()

	_, data, err := c.client.PostForm(c.baseURL.String(), nil)
	if err != nil {
		return nil, err
	}

	f := new(File)
	err = json.Unmarshal(data, f)
	if err != nil {
		return nil, err
	}

	return f, nil
}

type FileMeta struct {
	*File
	BlockList   string `json:"block_list"`
	IfHasSubDir uint   `json:"ifhassubdir"`
}

// 获取单个文件或目录的元信息。
func (c *Client) GetMeta(path string) (*FileMeta, error) {
	c.baseURL.Path = filepath.Join(c.baseURL.Path, "file")
	c.query.Set("method", "meta")
	c.query.Set("path", path)
	c.baseURL.RawQuery = c.query.Encode()

	_, data, err := c.client.Get(c.baseURL.String())
	if err != nil {
		return nil, err
	}

	f := new(FileMeta)
	err = json.Unmarshal(data, f)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// 批量获取文件/目录的元信息
func (c *Client) BatchGetMeta(paths []string) ([]*FileMeta, error) {
	c.baseURL.Path = filepath.Join(c.baseURL.Path, "file")

	if len(paths) == 0 {
		return nil, ErrInvalidArgument
	}

	tmp := make(map[string][]map[string]string, len(paths))
	for i, p := range paths {
		tmp["list"][i] = map[string]string{"path": p}
	}
	param, err = json.Marshal(tmp)
	if err != nil {
		return err
	}

	c.query.Set("method", "meta")
	c.query.Set("param", string(param))
	c.baseURL.RawQuery = c.query.Encode()

	_, data, err = c.client.PostForm(c.baseURL.String(), nil)
	if err != nil {
		return nil, err
	}

	metas := struct {
		List []*FileMeta `json:"list"`
	}{}
	err = json.Unmarshal(data, &metas)
	if err != nil {
		return nil, err
	}

	return metas.List, nil
}

// 获取目录下的文件列表
// order: “asc”或“desc”，缺省采用降序排序
// by: 排序字段，缺省根据文件类型排序：
//     - time（修改时间）
//     - name（文件名）
//     - size（大小，注意目录无大小）
// limit: 返回条目控制，参数格式为：n1-n2。
//        返回结果集的[n1, n2)之间的条目，缺省返回所有条目；n1从0开始。
func (c *Client) ListFiles(path, order, by, limit string) ([]*File, error) {
	c.baseURL.Path = filepath.Join(c.baseURL.Path, "file")

	c.query.Set("method", "list")
	c.query.Set("path", path)
	c.query.Set("by", by)
	c.query.Set("limit", limit)
	c.baseURL.RawQuery = c.query.Encode()
	_, data, err := c.client.PostForm(c.baseURL.String(), nil)
	if err != nil {
		return nil, err
	}

	files := struct {
		List []*File `json:"list"`
	}{}
	err = json.Unmarshal(data, &files)
	if err != nil {
		return nil, err
	}

	return files.List, nil
}

func (c *Client) fileOp(method string, args ...string) (*http.Response, []byte, error) {
	c.baseURL.Path = filepath.Join(c.baseURL.Path, "file")

	c.query.Set("method", method)

	switch method {
	case "move", "copy":
		c.query.Set("from", args[0])
		c.query.Set("to", args[1])
	case "delete":
		c.query.Set("path", args[0])
	}

	c.baseURL.Path = c.query.Encode()
	return c.client.PostForm(c.baseURL.String(), nil)
}

// 移动单个文件/目录
func (c *Client) Move(from, to string) error {
	_, data, err := c.fileOp("move", from, to)
	if err != nil {
		return err
	}
	//TODO: json unmarshal result.
	fmt.Println(string(data))

	return nil
}

// 拷贝单个文件/目录
func (c *Client) Copy(from, to string) error {
	_, data, err := c.fileOp("copy", from, to)
	if err != nil {
		return err
	}

	//TODO: json unmarshal result.
	fmt.Println(string(data))

	return nil
}

// 删除单个文件/目录
func (c *Client) Delete(path string) error {
	_, data, err := c.fileOp("delete", path)
	if err != nil {
		return err
	}

	fmt.Println(string(data))
	// TODO marshal result

	return nil
}

type Pair struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (c *Client) batchGeneric(method string, args ...interface{}) (*http.Response, []byte, error) {
	c.baseURL.Path = filepath.Join(c.baseURL.Path, "file")

	c.query.Set("method", method)

	var err error
	var param []byte

	switch method {
	case "move", "copy":
		tmp := struct {
			List []*Pair `json:"list"`
		}{
			List: args,
		}
		param, err = json.Marshal(&tmp)
	case "delete":
		tmp := struct {
			List []string `json:"list"`
		}{
			List: args,
		}
		param, err = json.Marshal(&tmp)
	}
	if err != nil {
		return err
	}

	d := url.Values{}
	d.Set("param", string(param))

	return c.client.PostForm(c.baseURL.String(), strings.NewReader(d.Encode()))
}

// 批量移动文件/目录
func (c *Client) BatchMove(pairs []*Pair) error {
	_, data, err := c.batchGeneric(pairs, "move")
	if err != nil {
		return err
	}

	// TODO unmarshal result
	fmt.Println(string(data))
	return nil
}

// 批量拷贝文件/目录
func (c *Client) BatchCopy(pairs []*Pair) error {
	_, data, err := c.batchGeneric(pairs, "copy")
	if err != nil {
		return err
	}

	// TODO unmarshal result
	fmt.Println(string(data))
	return nil
}

// 批量删除文件/目录
func (c *Client) BatchDelete(paths []string) error {
	_, data, err := c.batchGeneric("delete", paths)
	if err != nil {
		return err
	}

	fmt.Println(string(data))
	// TODO: unmarshal data
	return nil
}

// 按文件名搜索文件（不支持查找目录）。
func (c *Client) Search(path string, word string, recursive bool) ([]*File, error) {
	c.baseURL.Path = filepath.Join(c.baseURL.Path, "file")

	var re string
	if recursive {
		re = "1"
	} else {
		re = "0"
	}

	c.query.Set("method", "search")
	c.query.Set("path", path)
	c.query.Set("wd", word)
	c.query.Set("re", re)
	c.baseURL.RawQuery = c.query.Encode()

	_, data, err := c.client.Get(c.baseURL.String())
	if err != nil {
		return nil, err
	}

	files := struct {
		List []*File `json:"list"`
	}{}
	err = json.Unmarshal(data, &files)
	if err != nil {
		return nil, err
	}

	return files.List, nil
}

// **高级功能**

//获取指定图片文件的缩略图。
func (c *Client) Thumbnail() error {
	c.baseURL.Path = filepath.Join(c.baseURL.Path, "thumbnail")

	return nil

}

// 增量更新查询
func (c *Client) Diff() error {

	return nil
}

// 为当前用户进行视频转码并实现在线实时观看
func (c *Client) Streaming(path, typ string) error {

	return nil
}

type StreamFile struct {
	Total uint    `json:"total"`
	Start uint    `json:"start"`
	Limit uint    `json:"limit"`
	List  []*File `json:"list"`
}

// 获取流式文件列表
func (c *Client) ListStream(typ, start, limit, filterPath string) (*StreamFile, error) {
	c.baseURL.Path = filepath.Join(c.baseURL.Path, "stream")

	c.query.Set("method", "list")
	c.query.Set("tpye", typ)
	c.query.Set("start", start)
	c.query.Set("limit", limit)
	c.query.Set("filter_path", filterPath)
	c.baseURL.RawQuery = c.query.Encode()

	_, data, err := c.client.Get(c.baseURL.String())
	if err != nil {
		return nil, err
	}

	s := new(StreamFile)
	err = json.Unmarshal(data, s)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// 下载流式文件
func (c *Client) DownloadStream(path string) error {
	c.downloadURL.Path = filepath.Join(c.downloadURL.Path, "file")

	c.query.Set("method", "download")
	c.query.Set("path", path)
	c.downloadURL.RawQuery = c.query.Encode()

	_, _, err = c.client.Get(c.downloadURL.String())

	//TODO: 需注意处理好 302 跳转问题。

	return err
}

// 计算文件的各种值
func (c *Client) sumFile(path string) (contentLen int, contentMd5, contentCrc32, sliceMd5 string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", "", "", err
	}

	buf := &bytes.Buffer{}
	_, err = io.Copy(buf, f)
	if err != nil {
		return 0, "", "", "", err
	}

	// 1
	contentLen = buf.Len()

	// 2
	h := md5.New()
	h.Write(buf.Bytes())
	contentMd5 = fmt.Sprintf("%x", h.Sum(nil))

	// 3
	contentCrc32 = crc32.ChecksumIEEE(buf.Bytes())

	// 4
	slice := make([]byte, minRapidUploadFile)
	_, err = buf.Read(p)
	if err != nil {
		return 0, "", "", "", err
	}
	h.Reset()
	h.Write(slice)
	sliceMd5 = fmt.Sprintf("%x", h.Sum(nil))

	return contentLen, contentMd5, contentCrc32, sliceMd5, nil
}

// 秒传一个文件。
func (c *Client) RapidUpload(srcPath, targetPath string, overwrite bool) (*File, error) {
	c.baseURL.Path = filepath.Join(c.baseURL.Path, "file")

	contentLength, contentMd5, contentCrc32, sliceMd5, err := c.sumFile(srcPath)
	if err != nil {
		return nil, err
	}
	if contentLength <= minRapidUploadFile {
		return ErrMinRapidFileSize
	}

	c.query.Set("method", "rapidupload")
	c.query.Set("path", targetPath)
	c.query.Set("content-length", contentLength)
	c.query.Set("content-md5", contentMd5)
	c.query.Set("slice-md5", sliceMd5)
	c.query.Set("content-crc32", contentCrc32)
	c.query.Set("ondup", getOnDup(overwrite))

	c.baseURL.RawQuery = c.query.Encode()

	_, data, err := c.client.PostForm(c.baseURL.String(), nil)
	if err != nil {
		return nil, err
	}

	f := new(File)
	err = json.Unmarshal(data, f)
	if err != nil {
		return nil, err
	}

	return f, nil
}

// 添加离线下载任务
// savePath: 下载后的文件保存路径           必选
// sourceURL: 源文件的URL                  必选
// callback: 下载完毕后的回调，默认为空      可选
// expires: 请求失效时间，如果有，则会校验。  可选
// rateLimit: 下载限速，默认不限速
func (c *Client) AddOfflineDownloadTask(savePath, sourceURL, callback string, expires, rateLimit, timeout int64) (int64, error) {
	c.baseURL.Path = filepath.Join(c.baseURL.Path, "services/cloud_dl")

	c.query.Set("expires", expires)
	c.query.Set("save_path", savePath)
	c.query.Set("source_url", sourceURL)
	c.query.Set("rate_limit", strconv.FormatInt(int64(rateLimit), 10))
	c.query.Set("timeout", strconv.FormatInt(int64(timeout), 10))
	c.query.Set("callback", callback)

	c.baseURL.RawQuery = c.query.Encode()

	_, data, err := c.client.PostForm(c.baseURL.String(), nil)
	if err != nil {
		return 0, err
	}

	result := struct {
		TaskId int64 `json:"task_id"`
	}{}

	err = json.Unmarshal(data, &result)
	if err != nil {
		return 0, err
	}

	return result.TaskId, nil
}

// 精确查询离线下载任务
func (c *Client) QueryOfflineDownloadTask() {

}

// 查询离线下载任务列表
func (c *Client) ListOfflineDownloadTask() {

}

// 取消离线下载任务
func (c *Client) CancelOfflineDownloadTask() {

}

// **回收站相关**

// 查询回收站文件
func (c *Client) ListRecycle() {

}

// 还原单个文件或目录
func (c *Client) Restore() {

}

// 批量还原文件或目录
func (c *Client) BatchRestore() {

}

// 清空回收站
func (c *Client) EmptyRecycle() {

}
