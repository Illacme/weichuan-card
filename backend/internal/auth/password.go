package auth

import "golang.org/x/crypto/bcrypt"

// HashPassword 生成 bcrypt 哈希
func HashPassword(p string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword 校验明文与哈希
func CheckPassword(hash, p string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(p)) == nil
}

// dummyHash 一个固定的 bcrypt 哈希，唯一用途是「烧掉」与真实校验相同的 CPU 时间。
//
// 为什么需要它：账号不存在时如果直接返回，这条路径会比「密码错误」快几十毫秒
// （bcrypt cost=10 约 60ms）。这个差值本身就是可测量的信号 —— 把提示文案统一了
// 也没用，攻击者量响应时间照样能枚举出哪些账号存在。
//
// cost 必须与 HashPassword 一致（都用 DefaultCost），否则烧掉的时间对不上。
var dummyHash = func() []byte {
	b, err := bcrypt.GenerateFromPassword([]byte("weichuan-timing-equalizer"), bcrypt.DefaultCost)
	if err != nil {
		// GenerateFromPassword 只在口令超过 72 字节时失败，这里不可能触发。
		// 真触发了也不能让登录整体不可用，退回一个合法哈希（时间照样被烧掉）。
		return []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")
	}
	return b
}()

// CheckPasswordDummy 供「账号不存在」分支调用，执行一次等价代价的 bcrypt 比较，
// 让两条失败路径耗时不可区分。故意不返回结果：这个比较永远不该影响控制流。
func CheckPasswordDummy(p string) {
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(p))
}
