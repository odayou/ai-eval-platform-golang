package engine

// attachment_inject.go — attachment 消息类型(skill #32)。
// skill/agent 清单作为独立 attachment 消息注入(不进 system prompt)。

import "fmt"

type AttachmentMessage struct {
	Type      string
	Content   string
	IsInitial bool
}

type AttachmentInjector struct {
	attachments []AttachmentMessage
}

func NewAttachmentInjector() *AttachmentInjector {
	return &AttachmentInjector{}
}

func (i *AttachmentInjector) AddAttachment(a AttachmentMessage) {
	i.attachments = append(i.attachments, a)
}

func (i *AttachmentInjector) InjectIntoMessages() []map[string]string {
	out := make([]map[string]string, 0, len(i.attachments))
	for _, a := range i.attachments {
		var body string
		if a.IsInitial {
			body = fmt.Sprintf("[attachment:%s initial]\n%s", a.Type, a.Content)
		} else {
			body = fmt.Sprintf("[attachment:%s update]\n%s", a.Type, a.Content)
		}
		out = append(out, map[string]string{"role": "user", "content": body})
	}
	return out
}

func (i *AttachmentInjector) HasInitial() bool {
	for _, a := range i.attachments {
		if a.IsInitial {
			return true
		}
	}
	return false
}
