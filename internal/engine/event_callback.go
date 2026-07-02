package engine

// event_callback.go — 事件回调处理器(skill #44)。
// 自毁式一次性触发 + 串行执行 + 异常不中断。

import "sync"

type Event = AgentEvent

type EventCallback struct {
	Kind string
	Fn   func(Event) error
	Once bool
}

type EventCallbackProcessor struct {
	mu        sync.Mutex
	callbacks []EventCallback
}

func NewEventCallbackProcessor() *EventCallbackProcessor {
	return &EventCallbackProcessor{}
}

func (p *EventCallbackProcessor) Register(cb EventCallback) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callbacks = append(p.callbacks, cb)
}

func (p *EventCallbackProcessor) Process(event Event) {
	p.mu.Lock()
	pending := make([]EventCallback, 0, len(p.callbacks))
	kept := p.callbacks[:0]
	for _, cb := range p.callbacks {
		if cb.Kind == "" || cb.Kind == event.Type {
			pending = append(pending, cb)
			if !cb.Once {
				kept = append(kept, cb)
			}
		} else {
			kept = append(kept, cb)
		}
	}
	p.callbacks = kept
	p.mu.Unlock()

	for _, cb := range pending {
		func() {
			defer func() { _ = recover() }()
			_ = cb.Fn(event)
		}()
	}
}
