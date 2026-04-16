package upgrade

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	DefaultRepo       = "hulz413/fox-gateway"
	DefaultBinaryName = "fox-gateway"
)

var versionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+([-.][0-9A-Za-z.-]+)?$`)

func NormalizeVersionTag(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("version must not be empty")
	}
	if !versionPattern.MatchString(value) {
		return "", fmt.Errorf("invalid version %q: expected vX.Y.Z or X.Y.Z", value)
	}
	if strings.HasPrefix(value, "v") {
		return value, nil
	}
	return "v" + value, nil
}

func TargetSuffix(goos, goarch string) (string, error) {
	switch goos {
	case "linux":
		switch goarch {
		case "amd64":
			return "linux_amd64", nil
		default:
			return "", fmt.Errorf("unsupported Linux architecture: %s", goarch)
		}
	case "darwin":
		switch goarch {
		case "amd64":
			return "darwin_amd64", nil
		case "arm64":
			return "darwin_arm64", nil
		default:
			return "", fmt.Errorf("unsupported macOS architecture: %s", goarch)
		}
	default:
		return "", fmt.Errorf("unsupported operating system: %s", goos)
	}
}

func AssetName(binaryName, goos, goarch string) (string, error) {
	target, err := TargetSuffix(goos, goarch)
	if err != nil {
		return "", err
	}
	return binaryName + "_" + target, nil
}

func DownloadURL(repo, binaryName, tag, goos, goarch string) (string, error) {
	assetName, err := AssetName(binaryName, goos, goarch)
	if err != nil {
		return "", err
	}
	if tag != "" {
		return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, assetName), nil
	}
	return fmt.Sprintf("https://github.com/%s/releases/latest/download/%s", repo, assetName), nil
}

func CurrentDownloadURL(tag string) (string, error) {
	return DownloadURL(DefaultRepo, DefaultBinaryName, tag, runtime.GOOS, runtime.GOARCH)
}

func ResolveTargetVersion(ctx context.Context, client *http.Client, repo, requestedTag string) (string, error) {
	if strings.TrimSpace(requestedTag) != "" {
		return NormalizeVersionTag(requestedTag)
	}
	return ResolveLatestVersion(ctx, client, repo)
}

func ResolveLatestVersion(ctx context.Context, client *http.Client, repo string) (string, error) {
	if strings.TrimSpace(repo) == "" {
		repo = DefaultRepo
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	checkClient := *client
	checkClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://github.com/%s/releases/latest", repo), nil)
	if err != nil {
		return "", err
	}
	response, err := checkClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 300 || response.StatusCode >= 400 {
		return "", fmt.Errorf("resolve latest release version failed: %s", response.Status)
	}
	location := strings.TrimSpace(response.Header.Get("Location"))
	if location == "" {
		return "", fmt.Errorf("resolve latest release version failed: missing redirect location")
	}
	return extractTagFromReleaseLocation(location)
}

func extractTagFromReleaseLocation(location string) (string, error) {
	parsed, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	marker := "/releases/tag/"
	idx := strings.Index(parsed.Path, marker)
	if idx < 0 {
		return "", fmt.Errorf("resolve latest release version failed: unexpected redirect path %q", parsed.Path)
	}
	tag := parsed.Path[idx+len(marker):]
	if tag == "" {
		return "", fmt.Errorf("resolve latest release version failed: empty tag in redirect path")
	}
	return NormalizeVersionTag(tag)
}

func ResolveExecutablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	return path, nil
}

func DownloadAndReplace(ctx context.Context, client *http.Client, url, target string, progress io.Writer) error {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	total := fetchContentLength(ctx, client, url)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		message := strings.TrimSpace(string(body))
		if message == "" {
			return fmt.Errorf("download release binary failed: %s", response.Status)
		}
		return fmt.Errorf("download release binary failed: %s: %s", response.Status, message)
	}

	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(dir, ".fox-gateway-upgrade-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	mode := os.FileMode(0o755)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
		if mode&0o111 == 0 {
			mode |= 0o755
		}
	}
	if total <= 0 {
		total = response.ContentLength
	}
	if err := copyWithProgress(tmpFile, response.Body, total, progress); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return err
	}
	return os.Rename(tmpPath, target)
}

func fetchContentLength(ctx context.Context, client *http.Client, url string) int64 {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0
	}
	response, err := client.Do(request)
	if err != nil {
		return 0
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0
	}
	return response.ContentLength
}

func copyWithProgress(dst io.Writer, src io.Reader, total int64, progress io.Writer) error {
	if progress == nil {
		_, err := io.Copy(dst, src)
		return err
	}

	const chunkSize = 32 * 1024
	buffer := make([]byte, chunkSize)
	var downloaded int64
	lastLine := ""
	lastBytesReport := int64(0)

	for {
		n, readErr := src.Read(buffer)
		if n > 0 {
			if _, err := dst.Write(buffer[:n]); err != nil {
				return err
			}
			downloaded += int64(n)
			line := progressLine(downloaded, total, lastBytesReport)
			if line != "" && line != lastLine {
				fmt.Fprintf(progress, "\r%s", line)
				lastLine = line
			}
			if total <= 0 && downloaded-lastBytesReport >= 1024*1024 {
				lastBytesReport = downloaded
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				if total > 0 {
					fmt.Fprint(progress, "\rDownloading... 100%\n")
				} else if downloaded > 0 {
					fmt.Fprintf(progress, "\rDownloading... %d bytes\n", downloaded)
				}
				return nil
			}
			return readErr
		}
	}
}

func progressLine(downloaded, total, lastBytesReport int64) string {
	if total > 0 {
		percent := downloaded * 100 / total
		if percent > 100 {
			percent = 100
		}
		return fmt.Sprintf("Downloading... %3d%%", percent)
	}
	if downloaded-lastBytesReport >= 1024*1024 {
		return fmt.Sprintf("Downloading... %d bytes", downloaded)
	}
	return ""
}
