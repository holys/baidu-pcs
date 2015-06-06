package pcs

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

type Quota struct {
	Quota uint64 `json:"quota"`
	Used  uint64 `json:"used"`
}

// 获取当前用户空间配额信息
func (c *Client) GetQuota() (*Quota, *http.Response, error) {
	u, err := c.addOptions("quota", "info", nil)
	if err != nil {
		return nil, nil, err
	}

	quota := new(Quota)
	resp, err := c.Get(u, quota)
	if err != nil {
		return nil, resp, err
	}

	return quota, resp, nil
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

// path: 待上传文件的或者绝对路径/相对路径
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

	written, err := io.Copy(part, file)
	if err != nil {
		return nil, "", err
	}

	contentType := writer.FormDataContentType()
	writer.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, "", err
	}
	if written != stat.Size() {
		return nil, "", ErrIncompleteFile
	}

	return body, contentType, nil
}

type FileOptions struct {
	// 上传文件路径（含上传的文件名称)。
	Path string `url:"path"`

	// 可选值：
	// overwrite：表示覆盖同名文件；
	// newcopy：表示生成文件副本并进行重命名，命名规则为“文件名_日期.后缀”。
	OnDup string `url:"ondup,omitempty"`
}

// 上传单个文件
// srcPath: 待上传文件的或者绝对路径/相对路径
func (c *Client) Upload(srcPath string, opt *FileOptions) (*File, *http.Response, error) {
	body, contentType, err := c.upload(srcPath)
	if err != nil {
		return nil, nil, err
	}

	u, err := c.addOptions("file", "upload", opt)
	if err != nil {
		return nil, nil, err
	}

	f := new(File)
	resp, err := c.Post(u, contentType, body, f)
	if err != nil {
		return nil, resp, err
	}

	return f, resp, nil
}

// 分片上传—文件分片及上传
func (c *Client) BlockUpload(srcPath string) (*File, *http.Response, error) {
	body, contentType, err := c.upload(srcPath)
	if err != nil {
		return nil, nil, err
	}

	opt := struct {
		Type string `url:"type"`
	}{
		Type: "tmpfile",
	}

	u, err := c.addOptions("file", "upload", &opt)
	if err != nil {
		return nil, nil, err
	}

	f := new(File)
	resp, err := c.Post(u, contentType, body, f)
	if err != nil {
		return nil, resp, err
	}

	return f, resp, nil
}

// 分片上传—合并分片文件
// 与分片文件上传的upload方法配合使用，可实现超大文件（>2G）上传，同时也可用于断点续传的场景。
func (c *Client) CreateSuperFile(targetPath string, md5 []string, opt *FileOptions) (*File, *http.Response, error) {
	if len(md5) < 2 || len(md5) > 1024 {
		return nil, nil, ErrInvalidArgument
	}

	tmp := make(map[string][]string)
	tmp["blocklist"] = md5
	param, err := json.Marshal(tmp)
	if err != nil {
		return nil, nil, err
	}
	data := url.Values{}
	data.Set("param", string(param))

	u, err := c.addOptions("file", "createsuperfile", opt)
	if err != nil {
		return nil, nil, err
	}

	f := new(File)
	resp, err := c.PostForm(u, data, f)
	if err != nil {
		return nil, resp, err
	}

	return f, resp, nil
}

// 下载单个文件
// path: 下载文件路径，以/开头的绝对路径。
func (c *Client) Download(path string) (*http.Response, error) {
	opt := struct {
		Path string `url:"path"`
	}{
		Path: path,
	}
	u, err := c.addOptions("file", "download", &opt)
	if err != nil {
		return nil, err
	}

	resp, err := c.Get(u, nil)
	if err != nil {
		return resp, err
	}

	//TODO: save file to local
	return resp, nil
}

// 下载单个文件： 支持断点下载
// start: byte
// end: byte
func (c *Client) PartialDownload(path string, start, end int64) (*http.Response, error) {
	if start >= end {
		return nil, ErrInvalidArgument
	}
	opt := struct {
		Path string `url:"path"`
	}{
		Path: path,
	}
	u, err := c.addOptions("file", "download", &opt)
	if err != nil {
		return nil, err
	}

	req, err := c.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	ranges := fmt.Sprintf("bytes=%d-%d", start, end)
	req.Header.Set("Range", ranges)

	resp, err := c.Do(req, nil)
	if err != nil {
		return resp, err
	}

	//TODO: save file to local

	return resp, nil
}

// 创建目录
func (c *Client) Mkdir(path string) (*File, *http.Response, error) {
	opt := struct {
		Path string `url:"path"`
	}{
		Path: path,
	}

	u, err := c.addOptions("file", "mkdir", &opt)
	if err != nil {
		return nil, nil, err
	}

	f := new(File)
	resp, err := c.PostForm(u, nil, f)
	if err != nil {
		return nil, resp, err
	}

	return f, resp, nil
}

type FileMeta struct {
	*File
	BlockList   string `json:"block_list"`
	IfHasSubDir uint   `json:"ifhassubdir"`
}

// 获取单个文件或目录的元信息。
func (c *Client) GetMeta(path string) (*FileMeta, *http.Response, error) {
	opt := struct {
		Path string `url:"path"`
	}{
		Path: path,
	}

	u, err := c.addOptions("file", "meta", &opt)
	if err != nil {
		return nil, nil, err
	}

	f := new(FileMeta)
	resp, err := c.PostForm(u, nil, f)
	if err != nil {
		return nil, resp, err
	}

	return f, resp, nil
}

// 批量获取文件/目录的元信息
func (c *Client) BatchGetMeta(paths []string) ([]*FileMeta, *http.Response, error) {
	if len(paths) == 0 {
		return nil, nil, ErrInvalidArgument
	}

	u, err := c.addOptions("file", "meta", nil)
	if err != nil {
		return nil, nil, err
	}

	paramMap := make(map[string][]map[string]string)
	pathMap := make([]map[string]string, len(paths))
	for i, p := range paths {
		pathMap[i] = map[string]string{
			"path": p,
		}
	}
	paramMap["list"] = pathMap
	param, err := json.Marshal(paramMap)
	if err != nil {
		return nil, nil, err
	}

	data := url.Values{}
	data.Set("param", string(param))

	metas := struct {
		List []*FileMeta `json:"list"`
	}{}
	resp, err := c.PostForm(u, data, &metas)
	if err != nil {
		return nil, resp, err
	}

	return metas.List, resp, nil
}

type ListFilesOptions struct {
	// 需要list的目录，以/开头的绝对路径。
	Path string `url:"path"`

	// “asc”或“desc”，缺省采用降序排序。
	// asc（升序）
	// desc（降序）
	Order string `url:"order,omitempty"`

	// 排序字段，缺省根据文件类型排序：
	// time（修改时间）
	// name（文件名）
	// size（大小，注意目录无大小）
	By string `url:"by,omitempty"`

	// 返回条目控制，参数格式为：n1-n2。
	// 返回结果集的[n1, n2)之间的条目，缺省返回所有条目；n1从0开始。
	Limit string `url:"limit,omitempty"`
}

// 获取目录下的文件列表
func (c *Client) ListFiles(opt *ListFilesOptions) ([]*File, *http.Response, error) {
	u, err := c.addOptions("file", "list", opt)
	if err != nil {
		return nil, nil, err
	}

	files := struct {
		List []*File `json:"list"`
	}{}

	resp, err := c.Get(u, &files)
	if err != nil {
		return nil, resp, err
	}

	return files.List, resp, nil
}

type MoveCopyResponse struct {
	Extra struct {
		List []struct {
			To   string `json:"to"`
			From string `json:"from"`
		} `json:"list"`
	} `json:"extra"`
}

// 移动单个文件/目录
func (c *Client) Move(from, to string) (*MoveCopyResponse, *http.Response, error) {
	opt := struct {
		From string `url:"from"`
		To   string `url:"to"`
	}{from, to}

	u, err := c.addOptions("file", "move", &opt)
	if err != nil {
		return nil, nil, err
	}

	m := new(MoveCopyResponse)
	resp, err := c.PostForm(u, nil, m)
	if err != nil {
		return nil, resp, err
	}

	return m, resp, nil
}

// 拷贝单个文件/目录
func (c *Client) Copy(from, to string) (*MoveCopyResponse, *http.Response, error) {
	opt := struct {
		From string `url:"from"`
		To   string `url:"to"`
	}{from, to}

	u, err := c.addOptions("file", "copy", &opt)
	if err != nil {
		return nil, nil, err
	}

	m := new(MoveCopyResponse)
	resp, err := c.PostForm(u, nil, m)
	if err != nil {
		return nil, resp, err
	}

	return m, resp, nil
}

// 删除单个文件/目录
func (c *Client) Delete(path string) (*http.Response, error) {
	opt := struct {
		Path string `url:"path"`
	}{
		Path: path,
	}

	u, err := c.addOptions("file", "delete", opt)
	if err != nil {
		return nil, err
	}

	resp, err := c.PostForm(u, nil, nil)
	if err != nil {
		return resp, err
	}
	return resp, nil
}

type FTPair struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (c *Client) batchMoveCopyGeneric(method string, pairs []*FTPair) (*MoveCopyResponse, *http.Response, error) {
	u, err := c.addOptions("file", method, nil)
	if err != nil {
		return nil, nil, err
	}

	tmp := struct {
		List []*FTPair `json:"list"`
	}{
		List: pairs,
	}
	param, err := json.Marshal(&tmp)
	if err != nil {
		return nil, nil, err
	}

	data := url.Values{}
	data.Set("param", string(param))

	v := new(MoveCopyResponse)
	resp, err := c.PostForm(u, data, v)
	if err != nil {
		return nil, resp, err
	}

	return v, resp, nil
}

// 批量移动文件/目录
func (c *Client) BatchMove(pairs []*FTPair) (*MoveCopyResponse, *http.Response, error) {
	return c.batchMoveCopyGeneric("move", pairs)
}

// 批量拷贝文件/目录
func (c *Client) BatchCopy(pairs []*FTPair) (*MoveCopyResponse, *http.Response, error) {
	return c.batchMoveCopyGeneric("copy", pairs)
}

// 批量删除文件/目录
func (c *Client) BatchDelete(paths []string) (*http.Response, error) {
	u, err := c.addOptions("file", "delete", nil)
	if err != nil {
		return nil, err
	}
	tmp := struct {
		List []string `json:"list"`
	}{
		List: paths,
	}
	param, err := json.Marshal(&tmp)
	if err != nil {
		return nil, err
	}
	data := url.Values{}
	data.Set("param", string(param))
	resp, err := c.PostForm(u, data, nil)
	if err != nil {
		return resp, err
	}

	return resp, nil
}

type SearchOptions struct {
	// 需要检索的目录
	Path string `url:"path"`

	// 关键词
	Word string `url:"wd"`

	// 是否递归
	// “0”表示不递归
	// “1”表示递归
	// 缺省为“0”
	Re string `url:"re,omitempty"`
}

// 按文件名搜索文件（不支持查找目录）。
func (c *Client) Search(opt *SearchOptions) ([]*File, *http.Response, error) {
	u, err := c.addOptions("file", "search", opt)
	if err != nil {
		return nil, nil, err
	}

	files := struct {
		List []*File `json:"list"`
	}{}

	resp, err := c.Get(u, &files)
	if err != nil {
		return nil, resp, err
	}

	return files.List, resp, nil
}

// **高级功能**

type ThumbnailOptions struct {
	// 源图片的路径
	Path string `url:"path"`

	// 缩略图的质量，默认为“100”，取值范围(0,100]
	Quality int32 `url:"quality,omitempty"`

	// 指定缩略图的高度，取值范围为(0,1600]
	Height int `url:"height"`

	// 指定缩略图的宽度，取值范围为(0,1600]
	Width int `url:"width"`
}

//获取指定图片文件的缩略图
func (c *Client) Thumbnail(opt *ThumbnailOptions) (*http.Response, error) {
	u, err := c.addOptions("thumbnail", "generate", opt)
	if err != nil {
		return nil, err
	}

	resp, err := c.Get(u, nil)
	if err != nil {
		return resp, err
	}

	return resp, nil
}

// 增量更新查询
// cursor: 用于标记更新断点。
//  - 首次调用cursor=null；
//  - 非首次调用，使用最后一次调用diff接口的返回结果中的cursor。
func (c *Client) Diff(cursor string) (*http.Response, error) {
	opt := struct {
		Cursor string `url:"cursor"`
	}{
		Cursor: cursor,
	}

	u, err := c.addOptions("file", "diff", &opt)
	if err != nil {
		return nil, err
	}

	resp, err := c.Get(u, nil)
	if err != nil {
		return resp, err
	}

	//TODO: handle resp

	return resp, nil
}

// 为当前用户进行视频转码并实现在线实时观看
// path: 格式必须为m3u8,m3u,asf,avi,flv,gif,mkv,mov,mp4,m4a,3gp,3g2,mj2,mpeg,ts,rm,rmvb,webm
// typ: 目前支持以下格式：
//      M3U8_320_240、M3U8_480_224、M3U8_480_360、M3U8_640_480和M3U8_854_480
func (c *Client) Streaming(path, typ string) (*http.Response, error) {
	opt := struct {
		Path string `url:"path"`
		Type string `url:"type"`
	}{path, typ}
	u, err := c.addOptions("file", "streaming", &opt)
	if err != nil {
		return nil, err
	}

	resp, err := c.Get(u, nil)
	if err != nil {
		return resp, err
	}

	return resp, nil
}

type StreamFile struct {
	Total uint    `json:"total"`
	Start uint    `json:"start"`
	Limit uint    `json:"limit"`
	List  []*File `json:"list"`
}

type ListStreamOptions struct {
	// 类型分为video、audio、image及doc四种
	Type string `url:"type"`

	// 返回条目控制起始值，缺省值为0
	Start string `url:"start,omitempty"`

	// 返回条目控制长度，缺省为1000，可配置
	Limit string `url:"limit,omitempty"`

	// 需要过滤的前缀路径，如：/apps/album
	FilterPath string `url:"filter_path,omitempty"`
}

// 获取流式文件列表
func (c *Client) ListStream(opt *ListStreamOptions) (*StreamFile, *http.Response, error) {
	u, err := c.addOptions("stream", "list", opt)
	if err != nil {
		return nil, nil, err
	}

	sf := new(StreamFile)
	resp, err := c.Get(u, sf)
	if err != nil {
		return nil, resp, err
	}

	return sf, resp, nil
}

// 下载流式文件
func (c *Client) DownloadStream(path string) (*http.Response, error) {
	opt := struct {
		Path string `url:"path"`
	}{path}

	u, err := c.addOptions("file", "download", &opt)
	if err != nil {
		return nil, err
	}

	resp, err := c.Get(u, nil)
	if err != nil {
		return resp, err
	}

	// f, _ := os.Create("./test1.png")
	// f.Write(data)
	// f.Close()

	//TODO: 需注意处理好 302 跳转问题。

	return resp, nil
}

// 计算文件的各种值
func (c *Client) SumFile(path string) (contentLen int, contentMd5, sliceMd5 string, contentCrc32 uint32, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", "", 0, err
	}

	buf := &bytes.Buffer{}
	_, err = io.Copy(buf, f)
	if err != nil {
		return 0, "", "", 0, err
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
	_, err = buf.Read(slice)
	if err != nil {
		return 0, "", "", 0, err
	}
	h.Reset()
	h.Write(slice)
	sliceMd5 = fmt.Sprintf("%x", h.Sum(nil))

	return contentLen, contentMd5, sliceMd5, contentCrc32, nil
}

type RapiduUploadOptions struct {
	// 上传文件的全路径名
	Path string `url:"path"`

	// 待秒传的文件长度
	ContentLength int `url:"content-length"`

	// 待秒传的文件的MD5
	ContentMd5 string `url:"content-md5"`

	// 待秒传文件校验段的MD5
	SliceMd5 string `url:"slice-md5"`

	// 待秒传文件CRC32
	ContentCrc32 string `url:"content-crc32"`

	// overwrite：表示覆盖同名文件；
	// newcopy：表示生成文件副本并进行重命名，命名规则为“文件名_日期.后缀”。
	Ondup string `url:"ondup,omitempty"`
}

// 秒传一个文件。
func (c *Client) RapidUpload(opt *RapiduUploadOptions) (*File, *http.Response, error) {
	if opt.ContentLength <= minRapidUploadFile {
		return nil, nil, ErrMinRapidFileSize
	}
	u, err := c.addOptions("file", "rapidupload", opt)
	if err != nil {
		return nil, nil, err
	}

	f := new(File)
	resp, err := c.PostForm(u, nil, f)
	if err != nil {
		return nil, resp, err
	}

	return f, resp, nil
}

type AddTaskOptions struct {
	// 请求失效时间，如果有，则会校验
	Expires int `url:"expires,omitempty"`

	// 下载后的文件保存路径
	SavePath string `url:"save_path"`

	// 源文件的URL
	SourceURL string `url:"source_url"`

	// 下载限速，默认不限速
	RateLimit int `url:"rate_limit,omitempty"`

	// 下载超时时间，默认3600秒
	Timeout int `url:"timeout,omitempty"`

	// 下载完毕后的回调，默认为空
	Callback string `url:"callback,omitempty"`
}

// 添加离线下载任务
func (c *Client) AddOfflineDownloadTask(opt *AddTaskOptions) (int64, *http.Response, error) {
	u, err := c.addOptions("services/cloud_dl", "add_task", opt)
	if err != nil {
		return 0, nil, err
	}

	result := struct {
		TaskId int64 `json:"task_id"`
	}{}

	resp, err := c.PostForm(u, nil, &result)
	if err != nil {
		return 0, resp, err
	}
	return result.TaskId, resp, nil
}

type QueryTaskOptions struct {
	// 请求失效时间，如果有，则会校验
	Expires int `url:"expires,omitempty"`

	// 要查询的任务ID信息，如：1,2,3,4
	TaskIds string `url:"task_ids"`

	// 0：查任务信息
	// 1：查进度信息，默认为1
	OpType int `url:"op_type"`
}

// 精确查询离线下载任务
func (c *Client) QueryOfflineDownloadTask(opt *QueryTaskOptions) (*http.Response, error) {
	u, err := c.addOptions("service/cloud_dl", "query_task", opt)
	if err != nil {
		return nil, err
	}

	//TODO: handle response
	resp, err := c.PostForm(u, nil, nil)
	if err != nil {
		return resp, err
	}

	return resp, nil
}

type ListTaskOptions struct {
	// 请求失效时间，如果有，则会校验
	Expires int `url:"expires,omitempty"`

	// 查询任务起始位置，默认为0
	Start int `url:"start,omitempty"`

	// 设定返回任务数量，默认为10
	Limit int `url:"limit,omitempty"`

	// 0：降序，默认值
	// 1：升序
	Asc int `url:"asc,omitempty"`

	// 源地址URL，默认为空
	SourceURL string `url:"source_url,omitempty"`

	// 文件保存路径，默认为空
	SavePath string `url:"save_path,omitempty"`

	// 任务创建时间，默认为空
	CreateTime int `url:"create_time,omitempty"`

	// 任务状态，默认为空
	Status int `url:"status,omitempty"`

	// 是否需要返回任务信息:
	// 0：不需要
	// 1：需要，默认为1
	NeedTaskInfo int `url:"need_task_info,omitempty"`
}

// 查询离线下载任务列表
func (c *Client) ListOfflineDownloadTask(opt *ListTaskOptions) (*http.Response, error) {
	u, err := c.addOptions("service/cloud_dl", "list_task", opt)
	if err != nil {
		return nil, err
	}

	resp, err := c.PostForm(u, nil, nil)
	if err != nil {
		return resp, err
	}

	// TODO: unmarshal result
	return resp, nil
}

type CancelTaskOptions struct {
	// 请求失效时间，如果有，则会校验
	Expires int `url:"expires,omitempty"`

	// 要取消的任务ID号
	TaskId string `url:"task_id"`
}

// 取消离线下载任务
func (c *Client) CancelOfflineDownloadTask(opt *CancelTaskOptions) (*http.Response, error) {
	u, err := c.addOptions("service/cloud_dl", "cancel_task", opt)
	if err != nil {
		return nil, err
	}

	resp, err := c.PostForm(u, nil, nil)
	if err != nil {
		return resp, err
	}

	return resp, nil
}

// **回收站相关**

type ListRecycleOptions struct {
	// 返回条目的起始值，缺省值为0
	Start int `url:"start,omitempty"`

	// 返回条目的长度，缺省值为1000
	Limit int `url:"limit,omitempty"`
}

type ListRecycleResponse struct {
	List []*File `json:"list"`
}

// 查询回收站文件,获取回收站中的文件及目录列表
func (c *Client) ListRecycle(opt *ListRecycleOptions) (*ListRecycleResponse, *http.Response, error) {
	u, err := c.addOptions("file", "listrecycle", opt)
	if err != nil {
		return nil, nil, err
	}

	v := new(ListRecycleResponse)
	resp, err := c.Get(u, v)
	if err != nil {
		return nil, resp, err
	}

	return v, resp, nil
}

type RestoreResponse struct {
	Extra struct {
		List []struct {
			FsID string `json:"fs_id"`
		} `json:"list"`
	} `json:"extra"`
}

// 还原单个文件或目录
// fsId: 所还原的文件或目录在PCS的临时唯一标识ID
func (c *Client) Restore(fsId string) (*RestoreResponse, *http.Response, error) {
	opt := struct {
		FsId string `url:"fs_id"`
	}{fsId}

	u, err := c.addOptions("file", "restore", &opt)
	if err != nil {
		return nil, nil, err
	}

	v := new(RestoreResponse)
	resp, err := c.PostForm(u, nil, v)
	if err != nil {
		return nil, resp, err
	}

	return v, resp, nil
}

// 批量还原文件或目录
func (c *Client) BatchRestore(fsIds []string) (*RestoreResponse, *http.Response, error) {
	u, err := c.addOptions("file", "restore", nil)
	if err != nil {
		return nil, nil, err
	}

	paramMap := make(map[string][]map[string]string)
	fsIdMap := make([]map[string]string, len(fsIds))
	for i, p := range fsIds {
		fsIdMap[i] = map[string]string{
			"fs_id": p,
		}
	}
	paramMap["list"] = fsIdMap
	param, err := json.Marshal(paramMap)
	if err != nil {
		return nil, nil, err
	}

	d := url.Values{}
	d.Set("param", string(param))

	v := new(RestoreResponse)
	resp, err := c.PostForm(u, d, v)
	if err != nil {
		return nil, resp, err
	}

	return v, resp, nil
}

// 清空回收站
func (c *Client) EmptyRecycle() (*http.Response, error) {
	opt := struct {
		Type string `url:"type"`
	}{"recycle"}

	u, err := c.addOptions("file", "delete", &opt)
	if err != nil {
		return nil, err
	}

	resp, err := c.PostForm(u, nil, nil)
	if err != nil {
		return resp, err
	}

	return resp, nil
}
