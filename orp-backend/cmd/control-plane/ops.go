package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	serviceName = "orp"
	runCommand  = "_run"
)

type pidRecord struct {
	PID       int    `json:"pid"`
	Token     string `json:"token"`
	StartedAt string `json:"started_at"`
}

func runCLI(args []string) error {
	command := "help"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	if command != "help" && command != "--help" && command != "-h" && command != "version" && command != "--version" {
		if err := loadDotEnv(); err != nil {
			return err
		}
	}
	switch command {
	case "help", "--help", "-h":
		printUsage()
		return nil
	case "version", "--version":
		fmt.Printf("%s version %s (%s)\n", serviceName, version, commit)
		return nil
	case "start":
		return startService()
	case "stop":
		return stopService()
	case "restart":
		if err := stopService(); err != nil {
			return err
		}
		return startService()
	case "status":
		return showStatus()
	case "logs":
		return showLogs(args)
	case "run":
		return runServer()
	case runCommand:
		token, err := runID(args)
		if err != nil {
			return err
		}
		return runDaemon(token)
	default:
		printUsage()
		return fmt.Errorf("unknown command %q", command)
	}
}

func printUsage() {
	fmt.Printf(`ORP %s

用法：orp <命令>

命令：
  start      后台启动服务
  stop       优雅停止服务
  restart    重启服务
  status     查看进程状态
  logs       查看最近日志（logs -f 持续跟踪）
  run        前台运行服务，适用于 systemd、容器或调试
  version    查看版本
`, version)
}

func loadDotEnv() error {
	file, err := os.Open(".env")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open .env: %w", err)
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("read .env: %w", err)
	}
	for lineNumber, line := range strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf(".env:%d: expected KEY=VALUE", lineNumber+1)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf(".env:%d: empty environment variable name", lineNumber+1)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			} else {
				return fmt.Errorf(".env:%d: invalid quoted value", lineNumber+1)
			}
		} else if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			value = value[1 : len(value)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf(".env:%d: set %s: %w", lineNumber+1, key, err)
			}
		}
	}
	return nil
}

func statePaths() (string, string, error) {
	stateDir := strings.TrimSpace(os.Getenv("ORP_STATE_DIR"))
	if stateDir == "" {
		stateDir = "runtime"
	}
	if !filepath.IsAbs(stateDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", "", err
		}
		stateDir = filepath.Join(cwd, stateDir)
	}
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return "", "", fmt.Errorf("create state directory: %w", err)
	}
	return filepath.Join(stateDir, serviceName+".pid"), filepath.Join(stateDir, serviceName+".log"), nil
}

func runID(args []string) (string, error) {
	if len(args) != 1 || !strings.HasPrefix(args[0], "--run-id=") {
		return "", errors.New("internal daemon command requires a run id")
	}
	token := strings.TrimPrefix(args[0], "--run-id=")
	if len(token) != 32 {
		return "", errors.New("invalid daemon run id")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", errors.New("invalid daemon run id")
	}
	return token, nil
}

func startService() error {
	pidPath, logPath, err := statePaths()
	if err != nil {
		return err
	}
	if record, err := readPID(pidPath); err == nil {
		if processMatches(record) {
			fmt.Printf("%s 已在运行（PID %d）\n", serviceName, record.PID)
			return nil
		}
		_ = os.Remove(pidPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(pidPath)
	}

	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return fmt.Errorf("generate daemon token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	command := exec.Command(executable, runCommand, "--run-id="+token)
	command.Stdout = logFile
	command.Stderr = logFile
	configureDaemonProcess(command)
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start service process: %w", err)
	}
	_ = logFile.Close()
	_ = command.Process.Release()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if record, err := readPID(pidPath); err == nil && record.Token == token {
			fmt.Printf("%s 已启动（PID %d），日志：%s\n", serviceName, record.PID, logPath)
			return nil
		}
		if !processIDExists(command.Process.Pid) {
			return fmt.Errorf("服务进程启动失败，请查看日志：%s", logPath)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("等待服务进程就绪超时，请查看日志：%s", logPath)
}

func runDaemon(token string) error {
	pidPath, _, err := statePaths()
	if err != nil {
		return err
	}
	record := pidRecord{PID: os.Getpid(), Token: token, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	file, err := os.OpenFile(pidPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create PID file: %w", err)
	}
	if err := json.NewEncoder(file).Encode(record); err != nil {
		_ = file.Close()
		_ = os.Remove(pidPath)
		return fmt.Errorf("write PID file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(pidPath)
		return fmt.Errorf("close PID file: %w", err)
	}
	defer removePIDFile(pidPath, token)
	return runServer()
}

func stopService() error {
	pidPath, _, err := statePaths()
	if err != nil {
		return err
	}
	record, err := readPID(pidPath)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Printf("%s 已停止\n", serviceName)
		return nil
	}
	if err != nil {
		_ = os.Remove(pidPath)
		fmt.Printf("%s 已停止（清理过期 PID 文件）\n", serviceName)
		return nil
	}
	if !processMatches(record) {
		_ = os.Remove(pidPath)
		fmt.Printf("%s 已停止（清理过期 PID 文件）\n", serviceName)
		return nil
	}
	if err := stopProcess(record.PID); err != nil {
		return fmt.Errorf("send stop signal to PID %d: %w", record.PID, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if !processMatches(record) {
			removePIDFile(pidPath, record.Token)
			fmt.Printf("%s 已停止\n", serviceName)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("等待 PID %d 停止超时", record.PID)
}

func showStatus() error {
	pidPath, _, err := statePaths()
	if err != nil {
		return err
	}
	record, err := readPID(pidPath)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !processMatches(record)) {
		fmt.Printf("%s 未运行\n", serviceName)
		return nil
	}
	if err != nil {
		fmt.Printf("%s 未运行（PID 文件无效）\n", serviceName)
		return nil
	}
	fmt.Printf("%s 正在运行（PID %d，启动时间 %s）\n", serviceName, record.PID, record.StartedAt)
	return nil
}

func showLogs(args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "-f" && args[0] != "--follow") {
		return errors.New("用法：orp logs [-f|--follow]")
	}
	_, logPath, err := statePaths()
	if err != nil {
		return err
	}
	if _, err := os.Stat(logPath); err != nil {
		return fmt.Errorf("日志文件不存在：%s", logPath)
	}
	return showLogsOS(logPath, len(args) == 1)
}

func readPID(path string) (pidRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pidRecord{}, err
	}
	var record pidRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return pidRecord{}, err
	}
	if record.PID <= 0 || record.Token == "" {
		return pidRecord{}, errors.New("invalid PID record")
	}
	return record, nil
}

func processMatches(record pidRecord) bool {
	return processMatchesOS(record)
}

func removePIDFile(path, token string) {
	record, err := readPID(path)
	if err == nil && record.Token == token {
		_ = os.Remove(path)
	}
}
