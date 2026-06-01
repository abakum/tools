package mobile

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"fyne.io/tools/cmd/fyne/internal/mobile/binres"
)

type aapt2Resource struct {
	Name string
	Data []byte
}

func ensureIconAttributes(manifestStr string) string {
	iconAttr := `        android:icon="@mipmap/icon"`
	roundIconAttr := `        android:roundIcon="@mipmap/icon"`

	applicationStart := strings.Index(manifestStr, `<application`)
	if applicationStart != -1 {
		appEnd := findTagEnd(manifestStr, applicationStart)
		if appEnd != -1 {
			appTag := manifestStr[applicationStart : appEnd+1]
			if !strings.Contains(appTag, `android:icon`) {
				indent := "\n        "
				newAppTag := strings.Replace(appTag, `>`, indent+iconAttr+`>`, 1)
				if !strings.Contains(newAppTag, `android:roundIcon`) {
					newAppTag = strings.Replace(newAppTag, `>`, indent+roundIconAttr+`>`, 1)
				}
				manifestStr = manifestStr[:applicationStart] + newAppTag + manifestStr[appEnd+1:]
			} else if !strings.Contains(appTag, `android:roundIcon`) {
				newAppTag := strings.Replace(appTag, `>`, "\n        "+roundIconAttr+`>`, 1)
				manifestStr = manifestStr[:applicationStart] + newAppTag + manifestStr[appEnd+1:]
			}
		}
	}

	activityPattern := `<activity`
	pos := 0
	for {
		activityStart := strings.Index(manifestStr[pos:], activityPattern)
		if activityStart == -1 {
			break
		}
		activityStart += pos
		activityEnd := findTagEnd(manifestStr, activityStart)
		if activityEnd == -1 {
			break
		}
		activityTag := manifestStr[activityStart : activityEnd+1]
		if strings.Contains(activityTag, `android:name="org.golang.app.GoNativeActivity"`) && !strings.Contains(activityTag, `android:icon`) {
			newActivityTag := strings.Replace(activityTag, `>`, "\n        "+iconAttr+`>`, 1)
			manifestStr = manifestStr[:activityStart] + newActivityTag + manifestStr[activityEnd+1:]
		}
		pos = activityEnd + 1
	}

	return manifestStr
}

func findTagEnd(s string, start int) int {
	inQuote := false
	quoteChar := rune(0)
	for i := start; i < len(s); i++ {
		c := s[i]
		if c == '"' || c == '\'' {
			if !inQuote {
				inQuote = true
				quoteChar = rune(c)
			} else if rune(c) == quoteChar {
				inQuote = false
			}
		} else if c == '>' && !inQuote {
			return i
		}
	}
	return -1
}

// compileManifestAAPT2 converts a text AndroidManifest.xml to binary format via aapt2.
// Returns the binary manifest data and any additional resources (resources.arsc, res/*)
// from the aapt2 output APK.
//
// If dir/res/ exists, aapt2 receives it as a resource directory. This allows users to reference
// any resources from the manifest: android:icon="@mipmap/icon", android:theme="@style/MyTheme", etc.
func compileManifestAAPT2(manifestData []byte, dir, iconPath string, target int) ([]byte, []aapt2Resource, error) {
	androidHome := os.Getenv("ANDROID_HOME")
	if androidHome == "" {
		return nil, nil, fmt.Errorf("ANDROID_HOME env var not set")
	}

	// Find aapt2
	aapt2, err := findLastDir(filepath.Join(androidHome, "build-tools"))
	if err != nil {
		return nil, nil, fmt.Errorf("finding Android build-tools for aapt2: %v", err)
	}
	aapt2 = filepath.Join(aapt2, "aapt2")

	// Find android.jar
	platform, err := findLastDir(filepath.Join(androidHome, "platforms"))
	if err != nil {
		return nil, nil, fmt.Errorf("finding Android platform for android.jar: %v", err)
	}
	androidJar := filepath.Join(platform, "android.jar")

	// Create temporary directory for aapt2
	tmpdir, err := os.MkdirTemp("", "fyne-aapt2-*")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tmpdir)

	aapt2Dir := filepath.Join(tmpdir, "aapt2")
	if err := os.MkdirAll(aapt2Dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("failed to create aapt2 temp directory: %v", err)
	}

	// Write manifest to temp file
	manifestFile := filepath.Join(aapt2Dir, "AndroidManifest.xml")

	// Modify manifest: remove existing uses-sdk and add our own
	manifestStr := string(manifestData)

	// Remove existing <uses-sdk> (including commented ones)
	manifestStr = regexp.MustCompile(`(?s)(<!--\s*)?<uses-sdk[^>]*/?(.*?)?(?:</uses-sdk>)?\s*(-->)?`).ReplaceAllString(manifestStr, "")

	// Inject our <uses-sdk>
	usesSDK := fmt.Sprintf(
		"\n    <uses-sdk\n        android:minSdkVersion=\"%d\"\n        android:targetSdkVersion=\"%d\" />\n",
		binres.MinSDK, target,
	)

	// Find </manifest> (with leading whitespace) and insert <uses-sdk> before it
	closingTag := "</manifest>"
	idx := strings.Index(manifestStr, closingTag)
	if idx == -1 {
		return nil, nil, fmt.Errorf("manifest does not contain closing </manifest> tag")
	}
	
	// Check if there's leading whitespace before the closing tag
	leadingWhitespace := ""
	startIdx := idx
	for startIdx > 0 && (manifestStr[startIdx-1] == ' ' || manifestStr[startIdx-1] == '\t' || manifestStr[startIdx-1] == '\n') {
		startIdx--
	}
	if startIdx < idx {
		leadingWhitespace = manifestStr[startIdx:idx]
	}
	
	// Insert uses-sdk before the closing tag (with proper indentation)
	manifestStr = manifestStr[:startIdx] + usesSDK + leadingWhitespace + closingTag + manifestStr[idx+len(closingTag):]

	resDir := filepath.Join(dir, "res")
	resMipmapDir := filepath.Join(resDir, "mipmap-xxxhdpi-v4")
	resIconPath := filepath.Join(resMipmapDir, "icon.png")

	hasResDir := false
	if info, err := os.Stat(resDir); err == nil && info.IsDir() {
		hasResDir = true
		if _, err := os.Stat(resIconPath); os.IsNotExist(err) && iconPath != "" {
			if err := os.MkdirAll(resMipmapDir, 0o755); err == nil {
				src, err := os.ReadFile(iconPath)
				if err == nil {
					os.WriteFile(resIconPath, src, 0o644)
				}
			}
		}
	} else if iconPath != "" {
		if err := os.MkdirAll(resMipmapDir, 0o755); err == nil {
			src, err := os.ReadFile(iconPath)
			if err == nil {
				os.WriteFile(resIconPath, src, 0o644)
				hasResDir = true
			}
		}
	}

	if hasResDir {
		manifestStr = ensureIconAttributes(manifestStr)
	}

	// Write modified manifest
	if err := os.WriteFile(manifestFile, []byte(manifestStr), 0o644); err != nil {
		return nil, nil, fmt.Errorf("failed to write manifest file: %v", err)
	}

	// Prepare aapt2 link command
	args := []string{
		"link",
		"--manifest", manifestFile,
		"-I", androidJar,
		"--auto-add-overlay",
		"-o", filepath.Join(aapt2Dir, "output.apk"),
	}

	// If dir/res/ exists, compile resources and pass to aapt2 link.
	// This allows users to reference resources from the manifest:
	//   android:icon="@mipmap/icon", android:theme="@style/MyTheme", etc.
	if info, err := os.Stat(resDir); err == nil && info.IsDir() {
		compiledZip := filepath.Join(aapt2Dir, "compiled_res.zip")
		compileArgs := []string{"compile", "--dir", resDir, "-o", compiledZip}
		compileCmd := exec.Command(aapt2, compileArgs...)
		if buildV {
			fmt.Fprintf(os.Stderr, "aapt2 compile: %s\n", strings.Join(compileCmd.Args, " "))
		}
		if out, err := compileCmd.CombinedOutput(); err != nil {
			return nil, nil, fmt.Errorf("aapt2 compile failed: %v\n%s", err, string(out))
		}
		args = append(args, "-R", compiledZip)
	}

	// Run aapt2
	cmd := exec.Command(aapt2, args...)
	cmd.Dir = dir // Set working directory to project root for relative paths
	fmt.Fprintf(os.Stderr, "aapt2: %s\n", strings.Join(cmd.Args, " "))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, nil, fmt.Errorf("aapt2 failed: %v\n%s", err, string(output))
	}

	// Extract AndroidManifest.xml from output.apk
	outputApk := filepath.Join(aapt2Dir, "output.apk")
	zipReader, err := zip.OpenReader(outputApk)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open aapt2 output APK: %v", err)
	}
	defer zipReader.Close()

	var manifestBytes []byte
	var resources []aapt2Resource
	for _, file := range zipReader.File {
		if file.Name == "AndroidManifest.xml" {
			rc, err := file.Open()
			if err != nil {
				return nil, nil, fmt.Errorf("failed to open AndroidManifest.xml in APK: %v", err)
			}
			manifestBytes, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read AndroidManifest.xml: %v", err)
			}
			continue
		}
		if file.Name == "resources.arsc" || strings.HasPrefix(file.Name, "res/") {
			rc, err := file.Open()
			if err != nil {
				return nil, nil, fmt.Errorf("failed to open %s in APK: %v", file.Name, err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read %s: %v", file.Name, err)
			}
			resources = append(resources, aapt2Resource{Name: file.Name, Data: data})
		}
	}

	if manifestBytes == nil {
		return nil, nil, fmt.Errorf("AndroidManifest.xml not found in aapt2 output APK")
	}

	return manifestBytes, resources, nil
}