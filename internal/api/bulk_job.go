package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/tkiet2105/bestphone-pppoe/internal/activity"
	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	"github.com/tkiet2105/bestphone-pppoe/internal/pppoe"
)

// Tạo session hàng loạt chạy NỀN, giãn cách + tự thử lại.
//
// Vì sao: BRAS FPT chặn khi quay số dồn dập — 13/10/2026 tạo 2–3s/phiên trên
// proxy1/2/3 bị cấp IP 169.254 134 lần (kể cả phiên macvlan), còn thí nghiệm
// proxy5 cách ~30s/phiên lên đủ 100 không lỗi. 70 phiên × 30s ≈ 35 phút → không
// giữ được 1 request HTTP, nên chạy nền và trả trạng thái job.

const (
	bulkGapMax = 600 // trần giãn cách tự tăng (giây)
	// bulkStopAfterFails — thất bại LIÊN TIẾP (đã thử lại hết) thì dừng job: BRAS
	// đang chặn, cố tiếp chỉ làm nó đá cả phiên đang có của khách.
	bulkStopAfterFails = 5
)

type bulkJob struct {
	mu         sync.Mutex
	cancel     context.CancelFunc
	LineId     uint      `json:"line_id"`
	Count      int       `json:"count"`
	Done       int       `json:"done"`
	Ok         int       `json:"ok"`
	Fail       int       `json:"fail"`
	Retries    int       `json:"retries"`
	GapSeconds int       `json:"gap_seconds"` // giãn cách hiện tại (tự tăng khi bị chặn)
	Status     string    `json:"status"`      // running | done | stopped | cancelled
	Note       string    `json:"note,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
	SessionIds []uint    `json:"session_ids"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func (j *bulkJob) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	return map[string]any{
		"line_id": j.LineId, "count": j.Count, "done": j.Done, "ok": j.Ok, "fail": j.Fail,
		"retries": j.Retries, "gap_seconds": j.GapSeconds, "status": j.Status, "note": j.Note,
		"last_error": j.LastError, "session_ids": append([]uint(nil), j.SessionIds...),
		"started_at": j.StartedAt, "finished_at": j.FinishedAt,
	}
}

var (
	bulkMu   sync.Mutex
	bulkJobs = map[uint]*bulkJob{} // line_id → job gần nhất
)

// bulkParams — đọc từ settings (trang Cài đặt), có mặc định an toàn.
type bulkParams struct {
	gap       time.Duration
	retryMax  int
	retryWait time.Duration
}

func loadBulkParams() bulkParams {
	return bulkParams{
		gap:       time.Duration(db.GetSettingInt("bulk_gap_seconds", 30)) * time.Second,
		retryMax:  db.GetSettingInt("bulk_retry_max", 3),
		retryWait: time.Duration(db.GetSettingInt("bulk_retry_wait_seconds", 60)) * time.Second,
	}
}

// bulkRetryable — lỗi do BRAS chặn/nghẽn tạm thời → đáng thử lại sau khi chờ.
// KHÔNG thử lại AuthNak (sai cred; quay liên tục có thể bị khoá tài khoản).
func bulkRetryable(msg string) bool {
	low := strings.ToLower(msg)
	if strings.Contains(low, "authnak") || strings.Contains(low, "authentication failed") {
		return false
	}
	for _, k := range []string{"cgnat", "169.254", "pado", "padi", "pads", "timeout", "did not come up", "padt", "terminated by peer", "lệch"} {
		if strings.Contains(low, k) {
			return true
		}
	}
	return false
}

// bulkThrottled — dấu hiệu BRAS đang chặn vì quay dày → nới giãn cách.
func bulkThrottled(msg string) bool {
	low := strings.ToLower(msg)
	return strings.Contains(low, "169.254") || strings.Contains(low, "cgnat") || strings.Contains(low, "pado")
}

// nextGap — hàm THUẦN: bị chặn → gấp đôi (tối đa bulkGapMax); thành công → về mức gốc.
func nextGap(cur, base time.Duration, throttled bool) time.Duration {
	if !throttled {
		return base
	}
	n := cur * 2
	if n > bulkGapMax*time.Second {
		n = bulkGapMax * time.Second
	}
	if n < base {
		n = base
	}
	return n
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// holdReconnect — job tự lo thử lại: đẩy lịch reconnect của watchdog ra xa để nó
// không quay chen giữa (phá giãn cách). Hết job vẫn lỗi thì trả lại lịch thường.
func holdReconnect(sid uint, d time.Duration) {
	at := time.Now().Add(d)
	db.DB.Model(&models.Session{}).Where("id = ?", sid).Update("next_reconnect_at", &at)
}

func runBulkJob(ctx context.Context, j *bulkJob, line models.Line, sub createSessionReq, p bulkParams) {
	gap := p.gap
	consecutiveFail := 0
	finish := func(status, note string) {
		now := time.Now()
		j.mu.Lock()
		j.Status, j.Note, j.FinishedAt = status, note, &now
		j.mu.Unlock()
		snap := j.snapshot()
		activity.Info(activity.CategorySession, "bulk_done",
			fmt.Sprintf("Tạo hàng loạt line #%d %s: %d/%d thành công, %d lỗi, %d lần thử lại%s",
				j.LineId, status, snap["ok"], j.Count, snap["fail"], snap["retries"], func() string {
					if note != "" {
						return " — " + note
					}
					return ""
				}()),
			activity.LineId(j.LineId), activity.F("ok", snap["ok"]), activity.F("fail", snap["fail"]))
		publishBulk(j)
	}

	for i := 0; i < j.Count; i++ {
		if i > 0 && !sleepCtx(ctx, gap) {
			finish("cancelled", "admin dừng")
			return
		}
		sess, _, err := createSessionAndDial(line, sub)
		var full *lineFullError
		if errors.As(err, &full) {
			finish("stopped", err.Error())
			return
		}
		if err != nil && sess == nil {
			// lỗi kỹ thuật trước khi quay (hết ppp unit/cổng, thiếu cred…) → dừng, báo rõ
			j.mu.Lock()
			j.Done++
			j.Fail++
			j.LastError = truncateErr(err.Error(), 240)
			j.mu.Unlock()
			finish("stopped", "lỗi không thử lại được: "+truncateErr(err.Error(), 120))
			return
		}
		throttled := err != nil && bulkThrottled(err.Error())
		if err != nil {
			holdReconnect(sess.Id, time.Hour) // ngay lập tức: watchdog tick 20s không được quay chen
		}
		wait := p.retryWait
		for r := 1; r <= p.retryMax && err != nil && bulkRetryable(err.Error()); r++ {
			log.Printf("[bulk] line %d session %d lỗi (%s) → thử lại %d/%d sau %s", line.Id, sess.Id, truncateErr(err.Error(), 80), r, p.retryMax, wait)
			j.mu.Lock()
			j.Retries++
			j.mu.Unlock()
			publishBulk(j)
			if !sleepCtx(ctx, wait) {
				finish("cancelled", "admin dừng")
				return
			}
			// Dial tự ngắt pppd cũ + đổi unit nếu bị chiếm (manager.go)
			err = pppoe.M.Dial(sess.Id)
			wait *= 2
		}
		if err != nil && sess != nil {
			holdReconnect(sess.Id, 10*time.Minute) // trả lại cho watchdog nhưng không quay ngay
		}

		j.mu.Lock()
		j.Done++
		if sess != nil {
			j.SessionIds = append(j.SessionIds, sess.Id)
		}
		if err == nil {
			j.Ok++
			consecutiveFail = 0
		} else {
			j.Fail++
			j.LastError = truncateErr(err.Error(), 240)
			consecutiveFail++
		}
		gap = nextGap(gap, p.gap, throttled)
		j.GapSeconds = int(gap / time.Second)
		j.mu.Unlock()
		publishBulk(j)

		if consecutiveFail >= bulkStopAfterFails {
			finish("stopped", fmt.Sprintf("%d phiên liên tiếp thất bại sau khi đã thử lại — BRAS đang chặn, dừng để không làm rớt phiên của khách", consecutiveFail))
			return
		}
	}
	finish("done", "")
}

func publishBulk(j *bulkJob) {
	if eventHub != nil {
		eventHub.Publish("bulk.progress", j.snapshot())
	}
}

// startBulkJob — một line chỉ chạy 1 job tại một thời điểm.
func startBulkJob(line models.Line, count int, sub createSessionReq) (*bulkJob, error) {
	bulkMu.Lock()
	defer bulkMu.Unlock()
	if old, ok := bulkJobs[line.Id]; ok {
		old.mu.Lock()
		running := old.Status == "running"
		old.mu.Unlock()
		if running {
			return nil, fmt.Errorf("line #%d đang có job tạo hàng loạt chạy (%d/%d) — chờ xong hoặc dừng job trước", line.Id, old.Done, old.Count)
		}
	}
	p := loadBulkParams()
	ctx, cancel := context.WithCancel(context.Background())
	j := &bulkJob{cancel: cancel, LineId: line.Id, Count: count, GapSeconds: int(p.gap / time.Second),
		Status: "running", StartedAt: time.Now(), SessionIds: []uint{}}
	bulkJobs[line.Id] = j
	activity.Info(activity.CategorySession, "bulk_start",
		fmt.Sprintf("Bắt đầu tạo %d session cho line #%d: cách %s/phiên, thử lại tối đa %d lần (chờ %s, gấp đôi mỗi lần)",
			count, line.Id, p.gap, p.retryMax, p.retryWait),
		activity.LineId(line.Id), activity.F("count", count))
	go runBulkJob(ctx, j, line, sub, p)
	return j, nil
}

func bulkJobOf(lineID uint) *bulkJob {
	bulkMu.Lock()
	defer bulkMu.Unlock()
	return bulkJobs[lineID]
}
