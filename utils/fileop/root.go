package fileop

import (
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

const (
	appName        = "peopleAgent"
	appConfigDir   = "config" // 配置文件子目录
	appDataDir     = "data"   // 应用数据子目录
	appCacheDir    = "cache"  // 缓存子目录
	appLogsDir     = "logs"   // 日志子目录
	appStateDir    = "state"  // 状态子目录
	maxSearchDepth = 10       // 向上搜索项目根目录的最大深度
)

// RuntimeRoot returns the best runtime root for relative app paths.
// During local development we prefer the repository root.
// For installed apps we fall back to a user-writable app support directory.
func RuntimeRoot() string {
	// 检查环境变量覆盖
	if root := os.Getenv("peopleAgent_HOME"); root != "" {
		return root
	}

	// 开发环境优先
	if root := detectProjectRoot(); root != "" {
		return root
	}

	// 生产环境回退到用户配置目录
	if root := appSupportRoot(); root != "" {
		return root
	}

	// 兜底到当前工作目录
	if wd, err := os.Getwd(); err == nil {
		return wd
	}

	return "."
}

// ConfigPath returns the path for a configuration file.
// On Linux: ~/.config/peopleAgent/config/filename
// On macOS: ~/Library/Application Support/peopleAgent/config/filename
// On Windows: %APPDATA%/peopleAgent/config/filename
func ConfigPath(filename string) string {
	return filepath.Join(xdg.ConfigHome, appName, appConfigDir, filename)
}

// DataPath returns the path for an application data file.
// On Linux: ~/.local/share/peopleAgent/data/filename
// On macOS: ~/Library/Application Support/peopleAgent/data/filename
// On Windows: %LOCALAPPDATA%/peopleAgent/data/filename
func DataPath(filename string) string {
	return filepath.Join(xdg.DataHome, appName, appDataDir, filename)
}

// CachePath returns the path for a cache file.
// On Linux: ~/.cache/peopleAgent/cache/filename
// On macOS: ~/Library/Caches/peopleAgent/cache/filename
// On Windows: %LOCALAPPDATA%/peopleAgent/cache/filename
func CachePath(filename string) string {
	return filepath.Join(xdg.CacheHome, appName, appCacheDir, filename)
}

// LogPath returns the path for a log file.
// On Linux: ~/.local/state/peopleAgent/logs/filename
// On macOS: ~/Library/Logs/peopleAgent/logs/filename
// On Windows: %LOCALAPPDATA%/peopleAgent/logs/filename
func LogPath(filename string) string {
	return filepath.Join(xdg.StateHome, appName, appLogsDir, filename)
}

// StatePath returns the path for a state file.
// On Linux: ~/.local/state/peopleAgent/state/filename
// On macOS: ~/Library/Application Support/peopleAgent/state/filename
// On Windows: %LOCALAPPDATA%/peopleAgent/state/filename
func StatePath(filename string) string {
	return filepath.Join(xdg.StateHome, appName, appStateDir, filename)
}

// ResolvePath resolves a relative path against the runtime root.
func ResolvePath(path string) string {
	if path == "" {
		return RuntimeRoot()
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(RuntimeRoot(), filepath.Clean(path))
}

// BootstrapWorkingDirectory moves the process working directory to the runtime root
// so the rest of the app can keep using existing relative paths.
func BootstrapWorkingDirectory() (string, error) {
	root := RuntimeRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return root, err
	}
	if err := os.Chdir(root); err != nil {
		return root, err
	}
	return root, nil
}

// EnsureDirectories creates all necessary application directories
func EnsureDirectories() error {
	dirs := []string{
		filepath.Join(xdg.ConfigHome, appName, appConfigDir),
		filepath.Join(xdg.DataHome, appName, appDataDir),
		filepath.Join(xdg.CacheHome, appName, appCacheDir),
		filepath.Join(xdg.StateHome, appName, appLogsDir),
		filepath.Join(xdg.StateHome, appName, appStateDir),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	return nil
}

func detectProjectRoot() string {
	// 首先从当前工作目录向上查找
	if wd, err := os.Getwd(); err == nil {
		if root := searchUpwards(wd); root != "" {
			return root
		}
	}

	// 如果从当前目录没找到，从可执行文件路径向上查找
	exe, err := os.Executable()
	if err != nil {
		return ""
	}

	if realExe, err := filepath.EvalSymlinks(exe); err == nil {
		exe = realExe
	}

	return searchUpwards(filepath.Dir(exe))
}

// searchUpwards 从给定目录向上搜索项目根目录
func searchUpwards(startDir string) string {
	dir := startDir
	for i := 0; i < maxSearchDepth; i++ {
		if looksLikeProjectRoot(dir) {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	return ""
}

func looksLikeProjectRoot(dir string) bool {
	if dir == "" {
		return false
	}

	// 检查多种可能的项目标识文件
	markers := []string{
		"go.mod",
		"go.sum",
		"wails.json",
		".git",
		"package.json",
		"Cargo.toml",
		".hg",
		".bzr",
	}

	for _, marker := range markers {
		if hasPath(filepath.Join(dir, marker)) {
			return true
		}
	}
	return false
}

func hasPath(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func appSupportRoot() string {
	// 使用 XDG 配置目录作为应用支持目录
	return filepath.Join(xdg.ConfigHome, appName)
}
