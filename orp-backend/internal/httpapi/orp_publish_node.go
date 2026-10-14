package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const demoConfigRoot = "/etc/openresty/generated"
const demoNginx = "/usr/local/openresty/nginx/sbin/openresty"

var demoPublishLock sync.Mutex

type demoTarget struct {
	name, container, directory string
	healthPort                 int
}

func targetForNode(node orpDocument) (demoTarget, error) {
	if toString(node["host"]) != "127.0.0.1" {
		return demoTarget{}, errors.New("该节点尚未配置受控发布通道")
	}
	name := toString(node["name"])
	endpoint := toString(node["controlEndpoint"])
	switch name {
	case "openresty-east-1":
		if endpoint != "http://127.0.0.1:18081" {
			break
		}
		return demoTarget{name, "orp-openresty-east-1", "native-config", 18080}, nil
	case "openresty-east-2":
		if endpoint != "http://127.0.0.1:18181" {
			break
		}
		return demoTarget{name, "orp-openresty-east-2", "native-config-east-2", 18180}, nil
	case "openresty-east-3":
		if endpoint != "http://127.0.0.1:18281" {
			break
		}
		return demoTarget{name, "orp-openresty-east-3", "native-config-east-3", 18280}, nil
	}
	return demoTarget{}, errors.New("该节点尚未配置受控发布通道")
}

func demoNodeReady(ctx context.Context, node orpDocument) error {
	target, err := targetForNode(node)
	if err != nil {
		return err
	}
	output, err := exec.CommandContext(ctx, "docker", "inspect", "--format", `{{ index .Config.Labels "com.docker.compose.project" }} {{ index .Config.Labels "com.docker.compose.service" }}`, target.container).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "orp "+target.name {
		return errors.New("本地 OpenResty 示例容器不存在或身份不匹配")
	}
	return nil
}

func demoReleaseRoot(target demoTarget) (string, error) {
	path, err := filepath.Abs("../runtime/" + target.directory)
	if err != nil {
		return "", err
	}
	// The control plane is normally started from the repository root by dev.sh.
	if _, err := os.Stat("runtime/" + target.directory); err == nil {
		return filepath.Abs("runtime/" + target.directory)
	}
	if _, err := os.Stat(path); err != nil {
		return "", errors.New("找不到 OpenResty 共享配置目录")
	}
	return path, nil
}

func stageDemoRelease(target demoTarget, releaseID string, release orpRelease) error {
	root, err := demoReleaseRoot(target)
	if err != nil {
		return err
	}
	base := filepath.Join(root, "candidates")
	if err := os.MkdirAll(base, 0700); err != nil {
		return err
	}
	destination := filepath.Join(base, releaseID)
	if info, err := os.Lstat(destination); err == nil {
		if !info.IsDir() {
			return errors.New("候选制品目录已被替换")
		}
		for _, name := range sortedReleaseFiles(release.Files) {
			content, readErr := os.ReadFile(filepath.Join(destination, name))
			if readErr != nil || string(content) != release.Files[name] {
				return errors.New("候选制品内容与冻结快照不一致")
			}
		}
		content, readErr := os.ReadFile(filepath.Join(destination, "nginx.conf"))
		if readErr != nil || string(content) != release.Config {
			return errors.New("候选配置与冻结快照不一致")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	temporary, err := os.MkdirTemp(base, ".stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	for _, name := range sortedReleaseFiles(release.Files) {
		if filepath.Base(name) != name {
			return errors.New("制品文件名无效")
		}
		if err := os.WriteFile(filepath.Join(temporary, name), []byte(release.Files[name]), 0600); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(temporary, "nginx.conf"), []byte(release.Config), 0600); err != nil {
		return err
	}
	return os.Rename(temporary, destination)
}

func demoCommand(ctx context.Context, target demoTarget, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "docker", append([]string{"exec", target.container, demoNginx}, args...)...)
	output, err := command.CombinedOutput()
	if len(output) > 16*1024 {
		output = output[:16*1024]
	}
	return string(output), err
}

func validateDemoRelease(ctx context.Context, node orpDocument, releaseID string, release orpRelease) (string, error) {
	if err := demoNodeReady(ctx, node); err != nil {
		return "", err
	}
	target, _ := targetForNode(node)
	if err := stageDemoRelease(target, releaseID, release); err != nil {
		return "", err
	}
	output, err := demoCommand(ctx, target, "-t", "-c", demoConfigRoot+"/candidates/"+releaseID+"/nginx.conf")
	if err != nil {
		return output, err
	}
	// nginx -t validates syntax but does not bind sockets. The master is
	// already using its own management port; only the first such bind failure
	// needs to be rejected at precheck time.
	if strings.Contains(output, "bind() to ") && (strings.Contains(output, "Address already in use") || strings.Contains(output, "Address in use")) {
		return output, errors.New("监听端口已被占用，请调整端口或先停止占用该端口的服务")
	}
	return output, nil
}

// activateDemoRelease switches the stable path only after real target-node
// nginx -t. It restores the old symlink if reload or worker verification fails.
func activateDemoRelease(ctx context.Context, node orpDocument, releaseID string, release orpRelease) (string, error) {
	demoPublishLock.Lock()
	defer demoPublishLock.Unlock()
	target, err := targetForNode(node)
	if err != nil {
		return "", err
	}
	output, err := validateDemoRelease(ctx, node, releaseID, release)
	if err != nil {
		return output, fmt.Errorf("目标节点校验失败: %w", err)
	}
	root, err := demoReleaseRoot(target)
	if err != nil {
		return output, err
	}
	active := filepath.Join(root, "active")
	previous, err := os.Readlink(active)
	if err != nil {
		return output, errors.New("节点尚未使用稳定活动配置路径，请重建示例容器")
	}
	configTarget := demoConfigRoot + "/candidates/" + releaseID
	if err := swapDemoLink(active, configTarget); err != nil {
		return output, err
	}
	reloadOutput, reloadErr := demoCommand(ctx, target, "-s", "reload", "-c", demoConfigRoot+"/active/nginx.conf")
	output += reloadOutput
	if reloadErr == nil {
		reloadErr = waitDemoRelease(ctx, target, releaseID)
	}
	if reloadErr != nil {
		_ = swapDemoLink(active, previous)
		_, _ = demoCommand(ctx, target, "-s", "reload", "-c", demoConfigRoot+"/active/nginx.conf")
		return output, fmt.Errorf("节点未确认新版本已加载，已尝试恢复旧版本: %w", reloadErr)
	}
	return output, nil
}

func swapDemoLink(active, target string) error {
	temporary := active + ".next"
	_ = os.Remove(temporary)
	if err := os.Symlink(target, temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, active); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func waitDemoRelease(ctx context.Context, target demoTarget, releaseID string) error {
	client := &http.Client{Timeout: time.Second}
	lastObserved := "无响应"
	for attempt := 0; attempt < 15; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/__orp/release", target.healthPort), nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 128))
			response.Body.Close()
			if readErr == nil {
				lastObserved = fmt.Sprintf("HTTP %d，版本 %q", response.StatusCode, strings.TrimSpace(string(body)))
				if response.StatusCode == 200 && strings.TrimSpace(string(body)) == releaseID {
					return nil
				}
			} else {
				lastObserved = "读取探针响应失败: " + readErr.Error()
			}
		} else {
			lastObserved = "探针请求失败: " + err.Error()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("工作进程没有返回预期版本标识（期望 %s，最后观测：%s）", releaseID, lastObserved)
}

func observedDemoRelease(ctx context.Context, node orpDocument) (string, error) {
	target, err := targetForNode(node)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/__orp/release", target.healthPort), nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", fmt.Errorf("节点版本探针返回 HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 128))
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(body))
	if len(value) != 36 {
		return "", errors.New("节点未返回有效发布版本")
	}
	return value, nil
}
