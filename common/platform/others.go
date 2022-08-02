//go:build !windows
// +build !windows

package platform

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

func ExpandEnv(s string) string {
	return os.ExpandEnv(s)
}

func LineSeparator() string {
	return "\n"
}

func GetToolLocation(file string) string {
	const name = "v2ray.location.tool"
	toolPath := EnvFlag{Name: name, AltName: NormalizeEnvName(name)}.GetValue(getExecutableDir)
	return filepath.Join(toolPath, file)
}

// GetAssetLocation search for `file` in certain locations
func GetAssetLocation(file string) string {
	const name = "v2ray.location.asset"
	assetPath := NewEnvFlag(name).GetValue(getExecutableDir)
	defPath := filepath.Join(assetPath, file)

	//TEST: 测试代码
	log.Println("geoip目录相关路径: ", defPath)
	log.Println("geoip目录相关路径: ", assetPath)

	//FIXME: rain0 这里写了测试, 直接返回路径
	if file == "geoip.dat" || file == "geosite.dat" {
		var filename = "/Users/rain/Desktop/app/golang/v2ray-core/" + file
		if _, err := os.Stat(filename); err != nil && errors.Is(err, fs.ErrNotExist) {

		} else {
			return filename
		}
	}

	for _, p := range []string{
		defPath,
		filepath.Join("/usr/local/share/v2ray/", file),
		filepath.Join("/usr/share/v2ray/", file),
		filepath.Join("/opt/share/v2ray/", file),
	} {
		if _, err := os.Stat(p); err != nil && errors.Is(err, fs.ErrNotExist) {
			continue
		}

		// asset found
		return p
	}

	// asset not found, let the caller throw out the error
	return defPath
}
