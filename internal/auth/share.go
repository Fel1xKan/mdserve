package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"strconv"
	"strings"
	"sync"
	"time"
)

const shareVersion = "s1"

// IssueShare 生成一次性分享码。与 URL 链接不同，码只在创建时显示一次，
// 兑换后立刻失效；码本身不绑定文件，所以删除/改名不会留下后门。
func (a *Auth) IssueShare(host string) (string, time.Time) {
	exp := a.now().Add(a.shareTTL)
	payload := shareVersion + "." + strconv.FormatInt(exp.Unix(), 10) + "." + rand.Text()
	return payload + "." + a.sign("share", host, payload), exp
}

// RedeemShare 校验并消费分享码。签名对但已用过，返回 false。
func (a *Auth) RedeemShare(host, code string) bool {
	if code == "" || len(code) > 256 {
		return false
	}
	parts := strings.Split(code, ".")
	if len(parts) != 4 || parts[0] != shareVersion {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || a.now().Unix() >= exp {
		return false
	}
	payload := parts[0] + "." + parts[1] + "." + parts[2]
	if !hmac.Equal([]byte(a.sign("share", host, payload)), []byte(parts[3])) {
		return false
	}
	return a.used.markOnce(parts[2], time.Unix(exp, 0), a.now())
}

// usedSet 记录已消费的分享码 nonce。只在内存里，重启后清空；
// 因为分享码本身有 1 小时上限，重启窗口内的重放风险可接受。
type usedSet struct {
	mu    sync.Mutex
	seen  map[string]time.Time
	limit int
}

func newUsedSet(limit int) *usedSet {
	return &usedSet{seen: make(map[string]time.Time, 64), limit: limit}
}

// markOnce 在 key 首次出现时返回 true，并记下它的过期时间。
func (u *usedSet) markOnce(key string, exp, now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.seen[key]; ok {
		return false
	}
	u.seen[key] = exp
	u.sweepLocked(now)
	return true
}

func (u *usedSet) sweepLocked(now time.Time) {
	if len(u.seen) < u.limit {
		return
	}
	for k, exp := range u.seen {
		if now.After(exp) {
			delete(u.seen, k)
		}
	}
}
