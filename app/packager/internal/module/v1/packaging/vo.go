package packaging

// BuildReq 是打包接口的 JSON 请求体，四个字段都必须传入。
type BuildReq struct {
	URL            string   `json:"url"`              // 应用启动时加载的网页地址，传空字符串时内置 ZIP 资源。
	DominList      []string `json:"dominList"`        // 后端接口 origin 列表，最多 16 项。未配置固定版本地址时用于查询资源版本。
	Version        string   `json:"version"`          // 本次打包使用的静态资源版本，供应用启动时检查更新。
	LastVersionURL string   `json:"last_version_url"` // 资源 ZIP 下载地址。当 url 为空时必须提供，ZIP 根目录需有非空 index.html。
}

// BuildResp 描述已完成校验的 APK 产物。
type BuildResp struct {
	ID       string `json:"id"`                     // 构建作业 ID，由 32 个十六进制字符组成。
	FileName string `json:"fileName"`               // APK 文件名，格式为 <id>.apk。
	FileURL  string `json:"fileUrl"`                // APK 下载地址，为服务内下载路径或 MinIO 对外地址。
	Mode     string `json:"mode"`                   // 打包模式，url 表示加载网页地址，local 表示内置 ZIP 资源。
	Version  string `json:"version"`                // 本次写入 APK 的静态资源版本。
	SHA256   string `json:"bundleSha256,omitempty"` // 内置资源 ZIP 的 SHA-256 十六进制摘要，仅内置资源模式返回。
}
