package engine

// conversation_fork.go — 会话级 fork 父子树(skill #47)。
// parent_conversation_id + sub_conversation_ids + 继承 sandbox_id + 级联删除。

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
)

type ConversationTree struct {
	ID        string   `json:"id"`
	ParentID  string   `json:"parent_id,omitempty"`
	Children  []string `json:"sub_conversation_ids,omitempty"`
	SandboxID string   `json:"sandbox_id"`
}

type ConversationForest struct {
	mu    sync.Mutex
	trees map[string]*ConversationTree
}

func NewConversationForest() *ConversationForest {
	return &ConversationForest{trees: make(map[string]*ConversationTree)}
}

func (f *ConversationForest) AddRoot(id, sandboxID string) *ConversationTree {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &ConversationTree{ID: id, SandboxID: sandboxID}
	f.trees[id] = t
	return t
}

func (f *ConversationForest) Fork(parentID string) (*ConversationTree, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	parent, ok := f.trees[parentID]
	if !ok {
		return nil, fmt.Errorf("parent conversation %q not found", parentID)
	}
	child := &ConversationTree{
		ID:        uuid.New().String(),
		ParentID:  parentID,
		SandboxID: parent.SandboxID,
	}
	f.trees[child.ID] = child
	parent.Children = append(parent.Children, child.ID)
	return child, nil
}

func (f *ConversationForest) Get(id string) (*ConversationTree, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.trees[id]
	return t, ok
}

func (f *ConversationForest) CascadeDelete(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.trees {
		for i, c := range t.Children {
			if c == id {
				t.Children = append(t.Children[:i], t.Children[i+1:]...)
				break
			}
		}
	}
	var deleted []string
	var rec func(nodeID string)
	rec = func(nodeID string) {
		node, ok := f.trees[nodeID]
		if !ok {
			return
		}
		for _, child := range node.Children {
			rec(child)
		}
		delete(f.trees, nodeID)
		deleted = append(deleted, nodeID)
	}
	rec(id)
	return deleted
}
