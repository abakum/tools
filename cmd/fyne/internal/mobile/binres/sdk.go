package binres

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// MinSDK is the targeted sdk version for support by package binres.
const (
	// MinSDK = 15
	// platformBuildVersionName="4.0.4-1406430"
	MinSDK           = 23
	BuildVersionName = "6.0.1-2166767"
)

// Requires environment variable ANDROID_HOME to be set.
func apiResources() ([]byte, error) {
	apiResPath, err := apiResourcesPath()
	if err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(apiResPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(`%v; consider installing with "android update sdk --all --no-ui --filter android-%d"`, err, MinSDK)
		}
		return nil, err
	}
	defer zr.Close()

	buf := new(bytes.Buffer)
	for _, f := range zr.File {
		if f.Name == "resources.arsc" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			_, err = io.Copy(buf, rc)
			if err != nil {
				return nil, err
			}
			err = rc.Close()
			if err != nil {
				return nil, err
			}
			break
		}
	}
	if buf.Len() == 0 {
		return nil, fmt.Errorf("failed to read resources.arsc")
	}
	return buf.Bytes(), nil
}

func apiResourcesPath() (string, error) {
	sdkdir := os.Getenv("ANDROID_HOME")
	if sdkdir == "" {
		return "", fmt.Errorf("ANDROID_HOME env var not set")
	}
	platformsDir := filepath.Join(sdkdir, "platforms")
	entries, err := os.ReadDir(platformsDir)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no Android platforms found in %s", platformsDir)
	}
	sort.Slice(entries, func(i, j int) bool {
		vi := strings.TrimPrefix(entries[i].Name(), "android-")
		vj := strings.TrimPrefix(entries[j].Name(), "android-")
		ni, _ := strconv.Atoi(vi)
		nj, _ := strconv.Atoi(vj)
		return ni > nj
	})
	return filepath.Join(platformsDir, entries[0].Name(), "android.jar"), nil
}

// LatestAPI returns the API level of the latest installed Android platform.
func LatestAPI() (int, error) {
	p, err := apiResourcesPath()
	if err != nil {
		return 0, err
	}
	base := filepath.Base(filepath.Dir(p))
	verStr := strings.TrimPrefix(base, "android-")
	ver, err := strconv.Atoi(verStr)
	if err != nil {
		return 0, fmt.Errorf("cannot parse platform version from %q: %v", base, err)
	}
	return ver, nil
}

// PackResources produces a stripped down gzip version of the resources.arsc from api jar.
func PackResources() ([]byte, error) {
	tbl, err := OpenSDKTable()
	if err != nil {
		return nil, err
	}

	tbl.pool.strings = []string{} // should not be needed
	pkg := tbl.pkgs[0]

	// drop language string entries
	for _, typ := range pkg.specs[3].types {
		if typ.config.locale.language != 0 {
			for j, nt := range typ.entries {
				if nt == nil { // NoEntry
					continue
				}
				pkg.keyPool.strings[nt.key] = ""
				typ.indices[j] = NoEntry
				typ.entries[j] = nil
			}
		}
	}

	// drop strings from pool for specs to be dropped
	for _, spec := range pkg.specs[4:] {
		for _, typ := range spec.types {
			for _, nt := range typ.entries {
				if nt == nil { // NoEntry
					continue
				}
				// don't drop if there's a collision
				var collision bool
				for _, xspec := range pkg.specs[:4] {
					for _, xtyp := range xspec.types {
						for _, xnt := range xtyp.entries {
							if xnt == nil {
								continue
							}
							if collision = nt.key == xnt.key; collision {
								break
							}
						}
					}
				}
				if !collision {
					pkg.keyPool.strings[nt.key] = ""
				}
			}
		}
	}

	// entries are densely packed but probably safe to drop nil entries off the end
	for _, spec := range pkg.specs[:4] {
		for _, typ := range spec.types {
			var last int
			for i, nt := range typ.entries {
				if nt != nil {
					last = i
				}
			}
			typ.entries = typ.entries[:last+1]
			typ.indices = typ.indices[:last+1]
		}
	}

	// keeping 0:attr, 1:id, 2:style, 3:string
	pkg.typePool.strings = pkg.typePool.strings[:4]
	pkg.specs = pkg.specs[:4]

	bin, err := tbl.MarshalBinary()
	if err != nil {
		return nil, err
	}

	buf := new(bytes.Buffer)

	zw := gzip.NewWriter(buf)
	if _, err := zw.Write(bin); err != nil {
		return nil, err
	}
	if err := zw.Flush(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
