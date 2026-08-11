package managed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sourceProbeBytes   int64 = 64 << 10
	sourceProbeTimeout       = 12 * time.Second
)

type sourceProbe struct {
	url      string
	host     string
	duration time.Duration
	err      error
}

func selectSource(ctx context.Context, client *http.Client, artifact Artifact, report ProgressFunc) (string, string, error) {
	if len(artifact.URLs) == 0 {
		return "", "", errors.New("组件资产缺少公开下载候选源")
	}
	if report != nil {
		report(Progress{Component: artifact.Component, Version: artifact.Version, Phase: PhaseSelecting, Total: artifact.Size})
	}
	probeContext, cancel := context.WithTimeout(ctx, sourceProbeTimeout)
	defer cancel()
	results := make(chan sourceProbe, len(artifact.URLs))
	var workers sync.WaitGroup
	for _, candidate := range artifact.URLs {
		workers.Add(1)
		go func(raw string) {
			defer workers.Done()
			results <- probeSource(probeContext, client, raw, artifact.Size)
		}(candidate)
	}
	workers.Wait()
	close(results)
	var selected sourceProbe
	failures := make([]string, 0, len(artifact.URLs))
	for result := range results {
		if result.err != nil {
			failures = append(failures, result.host+": "+result.err.Error())
			continue
		}
		if selected.url == "" || result.duration < selected.duration {
			selected = result
		}
	}
	if selected.url == "" {
		return "", "", fmt.Errorf("组件 %s 的公开下载候选源全部不可用: %s", artifact.Component, strings.Join(failures, "; "))
	}
	return selected.url, selected.host, nil
}

func probeSource(ctx context.Context, client *http.Client, raw string, total int64) sourceProbe {
	parsed, err := url.Parse(raw)
	host := "未知主机"
	if err == nil {
		host = parsed.Hostname()
	}
	result := sourceProbe{url: raw, host: host}
	if err != nil {
		result.err = err
		return result
	}
	probeSize := min(sourceProbeBytes, total)
	if probeSize < 1 {
		result.err = errors.New("资产大小无效")
		return result
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		result.err = err
		return result
	}
	request.Header.Set("Range", fmt.Sprintf("bytes=0-%d", probeSize-1))
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		result.err = err
		return result
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent {
		result.err = fmt.Errorf("HTTP %d，不支持 Range", response.StatusCode)
		return result
	}
	expectedRange := fmt.Sprintf("bytes 0-%d/%d", probeSize-1, total)
	if response.Header.Get("Content-Range") != expectedRange {
		result.err = fmt.Errorf("Content-Range 无效: %s", response.Header.Get("Content-Range"))
		return result
	}
	read, err := io.Copy(io.Discard, io.LimitReader(response.Body, probeSize+1))
	if err != nil {
		result.err = err
		return result
	}
	if read != probeSize {
		result.err = fmt.Errorf("探测数据长度无效: %s/%s", strconv.FormatInt(read, 10), strconv.FormatInt(probeSize, 10))
		return result
	}
	result.duration = time.Since(started)
	return result
}
