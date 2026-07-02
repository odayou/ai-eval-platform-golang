package engine

// file_history.go — 文件级版本快照(skill #34)。
//
// 每次文件修改(FileEdit/FileWrite)时存版本快照,支持 undo/rewind。
// 设计参考 claw-code src/file_history.py。
//
// 存储:workspace/.file-history/<filepath_hash>/v001, v002, ...
// 索引:workspace/.file-history/index.jsonl(每行一条快照元数据)
//
// 折叠规则:连续修改同一文件时,只保留首次和最后一次(中间折叠)。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// FileSnapshot 文件版本快照元数据。
type FileSnapshot struct {
	ContentHash string    `json:"content_hash"` // sha256 前 16 位
	Version     int       `json:"version"`      // 版本号(1-based)
	Timestamp   time.Time `json:"timestamp"`
	Message     string    `json:"message"`      // 操作描述(如 "FileEdit", "FileWrite")
}

// fileHistoryDir 返回 .file-history 目录路径。
func fileHistoryDir(workspace string) string {
	return filepath.Join(workspace, ".file-history")
}

// hashFilePath 对文件路径做哈希(作为目录名,避免路径分隔符问题)。
func hashFilePath(filePath string) string {
	h := sha256.Sum256([]byte(filePath))
	return hex.EncodeToString(h[:])[:16]
}

// SaveFileSnapshot 保存文件版本快照。
// 在文件被修改前调用,保存修改前的内容。
// workspace 是工作区根目录,filePath 是相对工作区的文件路径。
func SaveFileSnapshot(workspace, filePath, oldContent, message string) error {
	if workspace == "" {
		return nil
	}

	histDir := filepath.Join(fileHistoryDir(workspace), hashFilePath(filePath))
	if err := os.MkdirAll(histDir, 0755); err != nil {
		return fmt.Errorf("创建 file-history 目录失败: %w", err)
	}

	// 计算 contentHash
	h := sha256.Sum256([]byte(oldContent))
	contentHash := hex.EncodeToString(h[:])[:16]

	// 确定版本号(目录下已有几个版本 + 1)
	entries, _ := os.ReadDir(histDir)
	version := len(entries) + 1

	// 折叠:如果上一个版本内容相同(连续无变化),跳过
	if version > 1 {
		prevPath := filepath.Join(histDir, fmt.Sprintf("v%03d", version-1))
		if prevContent, err := os.ReadFile(prevPath); err == nil {
			prevHash := sha256.Sum256(prevContent)
			if hex.EncodeToString(prevHash[:])[:16] == contentHash {
				return nil // 内容相同,跳过(折叠)
			}
		}
	}

	// 保存快照内容
	snapshotPath := filepath.Join(histDir, fmt.Sprintf("v%03d", version))
	if err := os.WriteFile(snapshotPath, []byte(oldContent), 0644); err != nil {
		return fmt.Errorf("写入快照失败: %w", err)
	}

	// 追加到索引
	indexEntry := FileSnapshot{
		ContentHash: contentHash,
		Version:     version,
		Timestamp:   time.Now(),
		Message:     message,
	}
	indexPath := filepath.Join(histDir, "index.jsonl")
	entryJSON, _ := json.Marshal(indexEntry)
	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil // 索引写入失败不影响主流程
	}
	defer f.Close()
	f.Write(append(entryJSON, '\n'))

	return nil
}

// GetFileSnapshot 读取指定版本的文件快照内容。
func GetFileSnapshot(workspace, filePath string, version int) (string, error) {
	histDir := filepath.Join(fileHistoryDir(workspace), hashFilePath(filePath))
	snapshotPath := filepath.Join(histDir, fmt.Sprintf("v%03d", version))
	content, err := os.ReadFile(snapshotPath)
	if err != nil {
		return "", fmt.Errorf("快照不存在: %w", err)
	}
	return string(content), nil
}

// ListFileSnapshots 列出文件的所有版本快照。
func ListFileSnapshots(workspace, filePath string) ([]FileSnapshot, error) {
	histDir := filepath.Join(fileHistoryDir(workspace), hashFilePath(filePath))
	indexPath := filepath.Join(histDir, "index.jsonl")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []FileSnapshot{}, nil
		}
		return nil, err
	}

	var snapshots []FileSnapshot
	for _, line := range splitLines(string(data)) {
		if line == "" {
			continue
		}
		var s FileSnapshot
		if json.Unmarshal([]byte(line), &s) == nil {
			snapshots = append(snapshots, s)
		}
	}
	return snapshots, nil
}

// splitLines 按换行符拆分(跨平台)。
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
