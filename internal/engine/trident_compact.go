package engine

// trident_compact.go — 三阶段语义压缩(skill #04)。
// supersede(旧版本合并) + collapse(闲聊折叠) + cluster(Jaccard 聚类)。

import (
	"fmt"
	"strings"
)

type CompactEvent struct {
	Role      string
	Content   string
	Timestamp string
	Type      string
}

func SupersedeEvents(events []CompactEvent) []CompactEvent {
	latest := make(map[string]int)
	var out []CompactEvent
	for _, ev := range events {
		if ev.Type != "read" && ev.Type != "write" && ev.Type != "delete" {
			out = append(out, ev)
			continue
		}
		key := ev.Type + "|" + ev.Content
		if idx, ok := latest[key]; ok {
			if ev.Timestamp > out[idx].Timestamp {
				out[idx] = ev
			}
		} else {
			latest[key] = len(out)
			out = append(out, ev)
		}
	}
	return out
}

func CollapseChatter(events []CompactEvent) []CompactEvent {
	var out []CompactEvent
	var run []CompactEvent
	flush := func() {
		if len(run) == 0 {
			return
		}
		if len(run) == 1 {
			out = append(out, run[0])
		} else {
			out = append(out, CompactEvent{
				Role: run[0].Role,
				Content: fmt.Sprintf("[%d 条闲聊折叠]", len(run)),
				Timestamp: run[0].Timestamp,
				Type: "collapsed_chatter",
			})
		}
		run = run[:0]
	}
	for _, ev := range events {
		c := strings.TrimSpace(strings.ToLower(ev.Content))
		isChat := ev.Type == "filler" || ev.Type == "ack" ||
			(len(c) <= 12 && (c == "ok" || c == "好的" || c == "嗯" || c == "收到" || c == "got it"))
		if isChat {
			run = append(run, ev)
			continue
		}
		flush()
		out = append(out, ev)
	}
	flush()
	return out
}

func ClusterSimilar(events []CompactEvent, threshold float64) []CompactEvent {
	if threshold <= 0 {
		threshold = 0.6
	}
	var out []CompactEvent
	for i := 0; i < len(events); {
		head := events[i]
		cluster := []CompactEvent{head}
		headTokens := tokens(head.Content)
		j := i + 1
		for ; j < len(events); j++ {
			if jaccardSim(headTokens, tokens(events[j].Content)) < threshold {
				break
			}
			cluster = append(cluster, events[j])
		}
		if len(cluster) == 1 {
			out = append(out, head)
		} else {
			head.Content = fmt.Sprintf("[聚类 %d 条相似] %s", len(cluster), head.Content)
			head.Type = "clustered"
			out = append(out, head)
		}
		i = j
	}
	return out
}

func tokens(s string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.Trim(w, ".,;:!?\"'()[]{}")
		if w != "" {
			set[w] = struct{}{}
		}
	}
	return set
}

func jaccardSim(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	inter := 0
	for w := range a {
		if _, ok := b[w]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
