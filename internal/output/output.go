package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/allank/psst/internal/cache"
	"github.com/allank/psst/internal/store"
)

var (
	styleLabel = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	styleHit   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	styleMiss  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleKey   = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	styleFaint = lipgloss.NewStyle().Faint(true)
)

func WriteLookupHit(w io.Writer, r *cache.Result) {
	score := ""
	if r.Match == "semantic" {
		score = fmt.Sprintf(" score=%.2f", r.Score)
	}
	fmt.Fprintf(w, "hit match=%s cached=%s expires=%s%s\n",
		r.Match,
		r.Entry.CachedAt.UTC().Format(time.RFC3339),
		r.Entry.ExpiresAt.UTC().Format(time.RFC3339),
		score,
	)
	fmt.Fprintf(w, "%s\n", string(r.Entry.Result))
}

func WriteLookupMiss(w io.Writer) {
	fmt.Fprintln(w, "miss")
}

func WriteLookupPretty(w io.Writer, r *cache.Result, hit bool) {
	if !hit {
		fmt.Fprintln(w, styleMiss.Render(" Cache miss"))
		return
	}
	score := ""
	if r.Match == "semantic" {
		score = fmt.Sprintf("  score=%.2f", r.Score)
	}
	fmt.Fprintf(w, "%s  match=%s%s\n",
		styleHit.Render(" Cache hit"),
		r.Match, score,
	)
	fmt.Fprintf(w, "%s  %s    %s %s\n",
		styleLabel.Render(" Cached:"),
		r.Entry.CachedAt.UTC().Format("2006-01-02 15:04"),
		styleLabel.Render("Expires:"),
		r.Entry.ExpiresAt.UTC().Format("2006-01-02 15:04"),
	)
	fmt.Fprintf(w, "\n %s\n", string(r.Entry.Result))
}

func WriteLookupJSON(w io.Writer, r *cache.Result, hit bool) {
	if !hit {
		fmt.Fprintln(w, `{"hit":false}`)
		return
	}
	var scoreVal any = nil
	if r.Match == "semantic" {
		scoreVal = r.Score
	}
	out := map[string]any{
		"hit":     true,
		"match":   r.Match,
		"score":   scoreVal,
		"cached":  r.Entry.CachedAt.UTC().Format(time.RFC3339),
		"expires": r.Entry.ExpiresAt.UTC().Format(time.RFC3339),
		"result":  json.RawMessage(r.Entry.Result),
	}
	enc, _ := json.Marshal(out)
	fmt.Fprintln(w, string(enc))
}

func WriteStored(w io.Writer, e *store.Entry) {
	fmt.Fprintf(w, "stored key=%s expires=%s\n",
		e.Key,
		e.ExpiresAt.UTC().Format(time.RFC3339),
	)
}

func WriteStoredPretty(w io.Writer, e *store.Entry) {
	fmt.Fprintf(w, "%s  key=%s  tool=%s  expires=%s\n",
		styleHit.Render(" Stored"),
		styleKey.Render(e.Key),
		e.Tool,
		e.ExpiresAt.UTC().Format("2006-01-02 15:04"),
	)
}

func WriteStoredJSON(w io.Writer, e *store.Entry) {
	out := map[string]any{
		"stored":  true,
		"key":     e.Key,
		"expires": e.ExpiresAt.UTC().Format(time.RFC3339),
	}
	enc, _ := json.Marshal(out)
	fmt.Fprintln(w, string(enc))
}

func WriteEvicted(w io.Writer, tool string, count int) {
	fmt.Fprintf(w, "evicted tool=%s count=%d\n", tool, count)
}

func WriteEvictedPretty(w io.Writer, tool string, count int) {
	fmt.Fprintf(w, "%s  tool=%s  count=%d\n",
		styleHit.Render(" Evicted"),
		tool, count,
	)
}

func WriteEvictedJSON(w io.Writer, tool string, count int) {
	out := map[string]any{
		"evicted": true,
		"tool":    tool,
		"count":   count,
	}
	enc, _ := json.Marshal(out)
	fmt.Fprintln(w, string(enc))
}

type StatusInfo struct {
	StorePath    string
	Entries      int
	Expired      int
	SizeBytes    int64
	ToolCounts   map[string]int
	DaemonAddr   string
	DaemonUptime string
}

func WriteStatus(w io.Writer, s *StatusInfo) {
	fmt.Fprintf(w, "store=%s entries=%d expired=%d size=%s\n",
		s.StorePath, s.Entries, s.Expired, humanSize(s.SizeBytes),
	)
	if s.DaemonAddr != "" {
		fmt.Fprintf(w, "daemon=%s uptime=%s\n", s.DaemonAddr, s.DaemonUptime)
	}
}

func WriteStatusPretty(w io.Writer, s *StatusInfo) {
	sep := strings.Repeat("─", 44)
	fmt.Fprintf(w, " %s\n %s\n", styleLabel.Render("psst Cache"), sep)
	fmt.Fprintf(w, " %-18s %s\n", styleLabel.Render("Store"), s.StorePath)
	fmt.Fprintf(w, " %-18s %d  %s\n",
		styleLabel.Render("Entries"),
		s.Entries,
		styleFaint.Render(fmt.Sprintf("(%d expired, pending eviction)", s.Expired)),
	)
	fmt.Fprintf(w, " %-18s %s\n", styleLabel.Render("Store size"), humanSize(s.SizeBytes))
	if len(s.ToolCounts) > 0 {
		var parts []string
		for t, n := range s.ToolCounts {
			parts = append(parts, fmt.Sprintf("%s (%d)", t, n))
		}
		fmt.Fprintf(w, " %-18s %s\n", styleLabel.Render("Tools cached"), strings.Join(parts, "  "))
	}
	if s.DaemonAddr != "" {
		fmt.Fprintf(w, " %-18s listening on %s  uptime=%s\n",
			styleLabel.Render("Daemon"), s.DaemonAddr, s.DaemonUptime,
		)
	}
}

func WriteStatusJSON(w io.Writer, s *StatusInfo) {
	out := map[string]any{
		"store":       s.StorePath,
		"entries":     s.Entries,
		"expired":     s.Expired,
		"size_bytes":  s.SizeBytes,
		"tool_counts": s.ToolCounts,
	}
	if s.DaemonAddr != "" {
		out["daemon_addr"] = s.DaemonAddr
		out["daemon_uptime"] = s.DaemonUptime
	}
	enc, _ := json.Marshal(out)
	fmt.Fprintln(w, string(enc))
}

func WriteInspectEntries(w io.Writer, entries []*store.Entry) {
	for _, e := range entries {
		fmt.Fprintf(w, "key=%s tool=%s args=%s cached=%s expires=%s\n",
			e.Key, e.Tool, string(e.Args),
			e.CachedAt.UTC().Format(time.RFC3339),
			e.ExpiresAt.UTC().Format(time.RFC3339),
		)
	}
}

func WriteInspectJSON(w io.Writer, entries []*store.Entry) {
	enc, _ := json.Marshal(entries)
	fmt.Fprintln(w, string(enc))
}

func WriteInspectPretty(w io.Writer, entries []*store.Entry) {
	for _, e := range entries {
		expired := ""
		if time.Now().After(e.ExpiresAt) {
			expired = styleMiss.Render(" [expired]")
		}
		keyDisplay := e.Key
		if len(keyDisplay) > 20 {
			keyDisplay = keyDisplay[:20] + "…"
		}
		fmt.Fprintf(w, " %s  %s%s\n   args=%s\n   cached=%s  expires=%s\n\n",
			styleKey.Render(keyDisplay),
			e.Tool, expired,
			string(e.Args),
			e.CachedAt.UTC().Format("2006-01-02 15:04"),
			e.ExpiresAt.UTC().Format("2006-01-02 15:04"),
		)
	}
}

func WriteRemoved(w io.Writer, paths []string) {
	for _, p := range paths {
		fmt.Fprintf(w, "removed %s\n", p)
	}
}

func humanSize(b int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
	)
	switch {
	case b >= MB:
		return fmt.Sprintf("%.1fMB", float64(b)/MB)
	case b >= KB:
		return fmt.Sprintf("%.1fKB", float64(b)/KB)
	default:
		return fmt.Sprintf("%dB", b)
	}
}

func Stderr(msg string) {
	fmt.Fprintln(os.Stderr, msg)
}
