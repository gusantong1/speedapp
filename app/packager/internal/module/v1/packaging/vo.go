package packaging

// BuildReq 是打包请求。四个字段都必须传入，URL 模式允许 last_version_url 为空。
type BuildReq struct {
	URL            string   `json:"url"`
	DominList      []string `json:"dominList"`
	Version        string   `json:"version"`
	LastVersionURL string   `json:"last_version_url"`
}

// BuildResp 返回已验证的 APK 及其下载地址。
type BuildResp struct {
	ID       string `json:"id"`
	FileName string `json:"fileName"`
	FileURL  string `json:"fileUrl"`
	Mode     string `json:"mode"`
	Version  string `json:"version"`
	SHA256   string `json:"bundleSha256,omitempty"`
}
