//go:build !(js && wasm)

package main

import "os"

// fileExists：供路由/首启判断使用；WASM 构建同样需要。
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
