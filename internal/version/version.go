package version

// Version 是服务端版本号。
//
// 编译时可通过 -ldflags 覆盖，发布流水线用它写入真实版本：
//
//	go build -ldflags "-X horizon/internal/version.Version=1.3.0"
//
// 留默认值是为了 `go run .` 和 go test 也能跑起来，不必记得传参数。
var Version = "1.3.0-dev"

// Commit 是构建对应的提交号，未注入时为空串。
var Commit = ""

// Full 返回用于展示的完整版本串。
func Full() string {
	if Commit == "" {
		return Version
	}
	return Version + "+" + Commit
}
