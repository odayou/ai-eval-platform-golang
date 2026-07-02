package engine

// user_context.go — 用户上下文注入器(skill #48)。
// 多租户隔离:UserContext + ADMIN 哨兵 + 路径前缀隔离。

import (
	"fmt"
	"path/filepath"
)

// UserContext 用户上下文(多租户隔离)。
type UserContext struct {
	UserID  string // 用户 ID(ADMIN 哨兵为空)
	IsAdmin bool   // 是否管理员
}

// AdminContext 创建管理员上下文(user_id=None 的哨兵)。
func AdminContext() UserContext {
	return UserContext{UserID: "", IsAdmin: true}
}

// NewUserContext 创建普通用户上下文。
func NewUserContext(userID string) UserContext {
	return UserContext{UserID: userID, IsAdmin: false}
}

// ScopedPath 返回用户隔离的文件路径(加 user_id 前缀)。
// 管理员不加前缀(可访问全局)。
func (uc UserContext) ScopedPath(basePath, filename string) string {
	if uc.IsAdmin || uc.UserID == "" {
		return filepath.Join(basePath, filename)
	}
	return filepath.Join(basePath, uc.UserID, filename)
}

// CheckPathAccess 检查用户是否有权访问路径(防越权)。
func (uc UserContext) CheckPathAccess(basePath, requestedPath string) error {
	if uc.IsAdmin {
		return nil // 管理员不受限
	}
	scoped := uc.ScopedPath(basePath, "")
	scopedAbs, _ := filepath.Abs(scoped)
	requestedAbs, _ := filepath.Abs(requestedPath)
	if !isSubpath(scopedAbs, requestedAbs) {
		return fmt.Errorf("权限拒绝:路径 %s 不在用户 %s 的隔离区内", requestedPath, uc.UserID)
	}
	return nil
}

// isSubpath 检查 child 是否在 parent 下。
func isSubpath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !startsWithDotDot(rel)
}

func startsWithDotDot(s string) bool {
	return len(s) >= 2 && s[0] == '.' && s[1] == '.'
}
