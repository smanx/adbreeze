package adb

// AppEntry 应用列表项。
// 基础字段来自 pm list 四连；version/minSdk/targetSdk 来自 dumpsys package 全量段解析；
// 占用三项来自 dumpsys diskstats 的按包数组（与 Package Names 顺序对齐）。
type AppEntry struct {
	Package     string `json:"package"`
	Label       string `json:"label,omitempty"`   // 应用名（内置映射兜底包名）
	VersionName string `json:"versionName,omitempty"`
	MinSdk      int    `json:"minSdk,omitempty"`
	TargetSdk   int    `json:"targetSdk,omitempty"`
	AppSize     uint64 `json:"appSize,omitempty"`   // 应用大小（字节）
	DataSize    uint64 `json:"dataSize,omitempty"`  // 数据大小
	CacheSize   uint64 `json:"cacheSize,omitempty"` // 缓存大小
	System      bool   `json:"system"`
	Disabled    bool   `json:"disabled"`
	ApkPath     string `json:"apkPath,omitempty"`
}

// AppInfo 单包详情（dumpsys package 懒加载解析）。
type AppInfo struct {
	Package      string `json:"package"`
	Label        string `json:"label,omitempty"`
	VersionName  string `json:"versionName,omitempty"`
	VersionCode  int    `json:"versionCode,omitempty"`
	MinSdk       int    `json:"minSdk,omitempty"`
	TargetSdk    int    `json:"targetSdk,omitempty"`
	ApkPath      string `json:"apkPath,omitempty"`
	FirstInstall string `json:"firstInstall,omitempty"`
	LastUpdate   string `json:"lastUpdate,omitempty"`
	System       bool   `json:"system"`
	Enabled      bool   `json:"enabled"`
}
