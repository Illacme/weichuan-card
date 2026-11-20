package handler

import (
	"net/http"
	"strconv"
	"strings"

	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
)

// 存储归属判定：这家企业的对象此刻到底落在谁的桶里。
//
// 只有一个真值源——storage_bindings 指向的存储档案（见 internal/storageprofile）。
// 历史上这里还并存过 tenant_storage_configs 的判定，第七十五轮随该表一起删除：
// 两套标准并存时必然出现「界面说在用客户桶、实际写平台盘」这类自相矛盾的结论，
// 而每一个请求都要为此多查一次库。
//
// 三条调用方都必须走这里，不要各写一份：
//   - 配额检查（checkStorageQuota）：用自有桶的企业不该被平台配额硬拦；
//   - 后台存储用量视图（storageQuotaView）：同上，否则会被画成满格；
//   - 后台「自有桶」状态展示。

// 存储配置的三种自检状态。
//
// 用「unknown」而不是空串作为初始值：后台列表要靠它区分「配好了没验」与
// 「验过了、是好的」。空串在这套 UI 里长得和「没有这个概念」一样，
// 读的人无法判断是该点自检还是该改配置。
const (
	storageStatusUnknown = "unknown"
	storageStatusOK      = "ok"
	storageStatusError   = "error"
)

// profileOwnerResolver 由三处启动入口注入（cmd/server、cmd/devserver、测试 harness）。
//
// 语义见 model.StorageOwner：ok=false 表示没有档案绑定（对象在平台盘）；
// ok=true 表示档案接管，Driver 为 local 时对象仍在平台盘（配额与平台域名仍适用）。
var profileOwnerResolver func(cid int64) (model.StorageOwner, bool)

// SetProfileOwnerResolver 注入档案归属判定（幂等，重复调用以最后一次为准）。
func SetProfileOwnerResolver(fn func(cid int64) (model.StorageOwner, bool)) {
	profileOwnerResolver = fn
}

// profileOwnerOf 是注入点的空安全封装：未接档案的部署与部分单测里它是 nil。
func profileOwnerOf(cid int64) (model.StorageOwner, bool) {
	if profileOwnerResolver == nil {
		return model.StorageOwner{}, false
	}
	return profileOwnerResolver(cid)
}

// tenantUsesOwnBucket 判断该租户此刻的文件是否真的写进自己的桶
// （即平台配额不应再硬拦截）。
//
// 判据只有一条：生效档案的驱动不是 local。绑定本身不是判据——
// 一家企业绑了本地档案，对象仍在平台盘上，平台配额照样该管。
func tenantUsesOwnBucket(cid int64) bool {
	if cid <= 0 {
		return false
	}
	owner, ok := profileOwnerOf(cid)
	if !ok {
		return false
	}
	return owner.UsesOwnBucket()
}

// maskCredential 把 AccessKey 打码成「前4****后4」。
//
// 对长度不足的极短值整串打码：AccessKey 至少 16 位，短到 8 位以内的一定是填错了，
// 这时候露前 4 后 4 等于把整个值露出来。
func maskCredential(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) == 0 {
		return ""
	}
	if len(r) <= 8 {
		return "******"
	}
	return string(r[:4]) + "****" + string(r[len(r)-4:])
}

// storageProbeLimitPerMin 存储自检接口每分钟每管理员的上限。
//
// 这是本项目少数几个「按管理员输入发起出向请求」的接口之一。
// 刻意**不套 urlprobe 的私网黑名单**：自建 MinIO 常在内网，套上就等于把主要
// 落地场景禁掉了。风险靠「只有平台超管能调」这一层控制，限流只用来防止误点放大。
const storageProbeLimitPerMin = 30

// storageProbeLimitFn 抽成包级变量：测试里连续多打几次不该撞 429。
var storageProbeLimitFn = func() int { return storageProbeLimitPerMin }

// storageTargetCompanyID 解析并校验路径里的企业 id。
//
// 0 不是合法值：0 在系统里表示「平台自身」，平台那份存储是进程级实例，
// 不该在绑定表里出现一行 tenant_id=0 以外的企业绑定。放行 0 会让绑定
// 写到全局默认那一行上，属于「看着保存成功、其实改了所有人」的静默错误。
func storageTargetCompanyID(c *gin.Context) (int64, bool) {
	cid, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || cid <= 0 {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "企业 id 不合法"})
		return 0, false
	}
	return cid, true
}
