package engine

import "testing"

func TestRecoveryRecipes(t *testing.T) {
	// 限流 → 应恢复
	r := AttemptRecovery("rate limit exceeded", nil)
	if r.Status != StatusPartial {
		t.Errorf("rate limit recovery = %v, want partial", r.Status)
	}

	// 认证失败 → 应升级
	r = AttemptRecovery("HTTP 401: unauthorized", nil)
	if r.Status != StatusEscalation {
		t.Errorf("auth error = %v, want escalation", r.Status)
	}

	// 超时 + 重试成功 → 应恢复
	retryCount := 0
	r = AttemptRecovery("context deadline exceeded", func() error {
		retryCount++
		return nil
	})
	if r.Status != StatusRecovered {
		t.Errorf("timeout retry = %v, want recovered", r.Status)
	}
	if retryCount != 1 {
		t.Errorf("retry count = %d, want 1", retryCount)
	}

	// 未知错误 → 升级
	r = AttemptRecovery("something weird happened", nil)
	if r.Status != StatusEscalation {
		t.Errorf("unknown error = %v, want escalation", r.Status)
	}
}

func TestExecPolicy(t *testing.T) {
	p := DefaultExecPolicy()

	if p.Evaluate("rm -rf /") != "deny" {
		t.Error("rm -rf / should be denied")
	}
	if p.Evaluate("git status") != "allow" {
		t.Error("git status should be allowed")
	}
	if p.Evaluate("rm temp.txt") != "ask" {
		t.Error("rm should ask")
	}
}

func TestLLMProfiles(t *testing.T) {
	// 用独立实例避免污染全局
	profiles := &LLMProfiles{
		profiles: make(map[string]*LLMProfile),
		maxNum:   10,
	}

	p1 := LLMProfile{Name: "fast", Model: "glm-4-flash"}
	p2 := LLMProfile{Name: "quality", Model: "deepseek-v4-pro"}

	if err := profiles.AddProfile(p1); err != nil {
		t.Fatal(err)
	}
	if err := profiles.AddProfile(p2); err != nil {
		t.Fatal(err)
	}

	// 第一个自动激活
	if active := profiles.GetActive(); active == nil || active.Name != "fast" {
		t.Error("first profile should be active")
	}

	// 切换
	usageID, err := profiles.SwitchProfile("quality")
	if err != nil {
		t.Fatal(err)
	}
	if len(usageID) != 16 {
		t.Errorf("usage_id length = %d, want 16", len(usageID))
	}
	if active := profiles.GetActive(); active == nil || active.Name != "quality" {
		t.Error("quality should be active after switch")
	}

	// 切换不存在的
	if _, err := profiles.SwitchProfile("nonexistent"); err == nil {
		t.Error("switch to nonexistent should error")
	}
}

func TestPromptCacheFingerprint(t *testing.T) {
	h1 := ComputeFingerprint("hello world")
	h2 := ComputeFingerprint("hello world")
	h3 := ComputeFingerprint("hello changed")

	if h1 != h2 {
		t.Error("same prompt should produce same hash")
	}
	if h1 == h3 {
		t.Error("different prompt should produce different hash")
	}
	if !DetectCacheBreak(h1, h3) {
		t.Error("should detect cache break")
	}
	if DetectCacheBreak("", h1) {
		t.Error("first time should not be cache break")
	}
}

func TestTridentCompact(t *testing.T) {
	// supersede
	events := []CompactEvent{
		{Type: "read", Content: "file.go", Timestamp: "1"},
		{Type: "read", Content: "file.go", Timestamp: "2"},
	}
	superseded := SupersedeEvents(events)
	if len(superseded) != 1 {
		t.Errorf("supersede: got %d, want 1", len(superseded))
	}

	// collapse
	chatter := []CompactEvent{
		{Role: "user", Content: "ok", Type: "message"},
		{Role: "user", Content: "好的", Type: "message"},
		{Role: "user", Content: "收到", Type: "message"},
		{Role: "user", Content: "请帮我写代码", Type: "message"},
	}
	collapsed := CollapseChatter(chatter)
	if len(collapsed) != 2 {
		t.Errorf("collapse: got %d, want 2", len(collapsed))
	}
}

func TestContextualFragment(t *testing.T) {
	store := NewFragmentStore()

	store.Add(ContextualFragment{
		ID: "system", Content: "You are a helpful assistant.",
		Priority: 100, Immutable: true,
	})
	store.Add(ContextualFragment{
		ID: "skills", Content: "Available skills: coding, review.",
		Priority: 50,
	})

	rendered := store.Render(10000)
	if rendered == "" {
		t.Error("render should not be empty")
	}
	if !contains(rendered, "system") {
		t.Error("render should contain system fragment")
	}

	// Immutable 片段不能被覆盖
	ok := store.Add(ContextualFragment{ID: "system", Content: "hacked"})
	if ok {
		t.Error("should not overwrite immutable fragment")
	}
}

func TestConversationFork(t *testing.T) {
	forest := NewConversationForest()
	forest.AddRoot("conv-1", "sandbox-1")

	child, err := forest.Fork("conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentID != "conv-1" {
		t.Error("child parent should be conv-1")
	}
	if child.SandboxID != "sandbox-1" {
		t.Error("child should inherit sandbox")
	}

	// 级联删除
	grandchild, _ := forest.Fork(child.ID)
	deleted := forest.CascadeDelete("conv-1")
	if len(deleted) != 3 {
		t.Errorf("cascade delete: got %d, want 3", len(deleted))
	}
	_ = grandchild
}

func TestAttachmentInjector(t *testing.T) {
	inj := NewAttachmentInjector()
	inj.AddAttachment(AttachmentMessage{Type: "skills", Content: "skill list", IsInitial: true})
	inj.AddAttachment(AttachmentMessage{Type: "skills", Content: "+ new skill", IsInitial: false})

	msgs := inj.InjectIntoMessages()
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if !contains(msgs[0]["content"], "initial") {
		t.Error("first should be initial")
	}
	if !contains(msgs[1]["content"], "update") {
		t.Error("second should be update")
	}
	if !inj.HasInitial() {
		t.Error("should have initial")
	}
}

func TestPluginSystem(t *testing.T) {
	registry := NewPluginRegistry("/tmp/nonexistent-plugins-dir")
	// 目录不存在,discover 应返回空不报错
	if err := registry.DiscoverPlugins(); err != nil {
		t.Errorf("discover on empty dir should not error: %v", err)
	}
	if len(registry.ListPlugins()) != 0 {
		t.Error("should have 0 plugins")
	}
}

func TestUserContext(t *testing.T) {
	admin := AdminContext()
	if !admin.IsAdmin {
		t.Error("admin should be IsAdmin")
	}

	user := NewUserContext("user-123")
	path := user.ScopedPath("/workspace", "file.go")
	if !contains(path, "user-123") {
		t.Error("scoped path should contain user_id")
	}

	// admin 不加前缀
	adminPath := admin.ScopedPath("/workspace", "file.go")
	if contains(adminPath, "user-123") {
		t.Error("admin path should not have user prefix")
	}
}

func TestConversationTrigger(t *testing.T) {
	if InferTrigger([]string{"github", "pr"}) != TriggerResolver {
		t.Error("github+pr should be resolver")
	}
	if InferTrigger([]string{"slack"}) != TriggerSlack {
		t.Error("slack should be slack")
	}
	if InferTrigger([]string{}) != TriggerManual {
		t.Error("empty should be manual")
	}
	label := TriggerLabel(TriggerSlack)
	if label != "Slack 集成" {
		t.Errorf("label = %q, want 'Slack 集成'", label)
	}
}
