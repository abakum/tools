// Copyright 2015 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mobile

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func compileCustomActivity(manifestDir string) ([]byte, error) {
	androidHome := os.Getenv("ANDROID_HOME")
	if androidHome == "" {
		return nil, fmt.Errorf("ANDROID_HOME not set (required to compile custom activity)")
	}

	platform, err := findLastDir(filepath.Join(androidHome, "platforms"))
	if err != nil {
		return nil, fmt.Errorf("finding Android platform for javac: %v", err)
	}
	buildTools, err := findLastDir(filepath.Join(androidHome, "build-tools"))
	if err != nil {
		return nil, fmt.Errorf("finding Android build-tools for d8: %v", err)
	}

	packagePath := filepath.Join(manifestDir, "org/golang/app/GoNativeActivity.java")
	javaSrcPath := manifestDir

	if _, err := os.Stat(packagePath); os.IsNotExist(err) {
		flatPath := filepath.Join(manifestDir, "GoNativeActivity.java")
		if _, err := os.Stat(flatPath); os.IsNotExist(err) {
			return nil, nil
		}

		tempDir := filepath.Join(tmpdir, "custom-activity")
		tempPackagePath := filepath.Join(tempDir, "org/golang/app")
		if err := os.MkdirAll(tempPackagePath, 0o755); err != nil {
			return nil, fmt.Errorf("creating temp package directory: %v", err)
		}
		tempFile := filepath.Join(tempPackagePath, "GoNativeActivity.java")
		if err := copyFile(tempFile, flatPath); err != nil {
			return nil, fmt.Errorf("copying activity file: %v", err)
		}
		packagePath = tempFile
		javaSrcPath = tempDir
	}

	classesDir := filepath.Join(tmpdir, "activity-classes")
	if err := os.MkdirAll(classesDir, 0o755); err != nil {
		return nil, err
	}

	javacCmd := exec.Command("javac",
		"-source", "1.8",
		"-target", "1.8",
		"-sourcepath", javaSrcPath,
		"-bootclasspath", filepath.Join(platform, "android.jar"),
		"-d", classesDir,
		packagePath,
	)
	if buildV {
		fmt.Fprintf(os.Stderr, "javac: %s\n", strings.Join(javacCmd.Args, " "))
	}
	if out, err := javacCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("javac failed: %v\n%s", err, string(out))
	}

	var classFiles []string
	filepath.WalkDir(classesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".class") {
			classFiles = append(classFiles, path)
		}
		return nil
	})
	if len(classFiles) == 0 {
		return nil, fmt.Errorf("no class files generated from custom activity")
	}

	dexOut := filepath.Join(tmpdir, "activity-dex")
	if err := os.MkdirAll(dexOut, 0o755); err != nil {
		return nil, err
	}

	d8Path := filepath.Join(buildTools, "d8")
	d8Cmd := exec.Command(d8Path, append([]string{"--output", dexOut}, classFiles...)...)
	if buildV {
		fmt.Fprintf(os.Stderr, "d8: %s\n", strings.Join(d8Cmd.Args, " "))
	}
	if out, err := d8Cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("d8 failed: %v\n%s", err, string(out))
	}

	dexPath := filepath.Join(dexOut, "classes.dex")
	dexData, err := os.ReadFile(dexPath)
	if err != nil {
		return nil, fmt.Errorf("reading activity dex: %v", err)
	}
	return dexData, nil
}