package refmigrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"weichuan/internal/storage"

	"gorm.io/gorm/schema"
)

// 本文件是 P3 的**执行器**：消费 plan 命令产出的同一份 JSON，不改计划、不重扫库。
//
// 为什么不能自己再扫一次库：计划是「某个时刻看到的库」，执行器重扫一次看到的是
// 「另一个时刻的库」。两者相等时看不出差别；一旦中间有人改了数据，重扫会把新值
// 当成计划的一部分搬走，而人手里那份计划（也是回滚的依据）说的完全是另一回事。
// 所以这里对每一条引用做**过期检测**：库里当前值必须等于计划里的 Raw，
// 或者等于 TargetKey 且台账有记录（上次已搬完）；除此之外一律停下。
//
// 三步的失败面刻意这样切：
//
//	① 复制对象（对象存储，不可回滚，只增不删）
//	② 改库 + 记台账（**同一个 MySQL 事务**，要么都成、要么都不成）
//
// ① 成功而 ② 失败 → 重跑时目标对象已存在且大小一致，跳过复制，只重试 ②。
// ① 失败 → 库一行都没动，重跑从 ① 开始。这就是「失败可重跑」的全部机制。

const (
	StatusApplied    = "applied"
	StatusRolledBack = "rolled_back"
)

var (
	// ErrNeedsHuman 是「命令跑通了，但数据/环境需要人处理」这一类错误的父错误。
	// 命令层据此给退出码 2（而不是 1）：1 让人去重跑命令，2 让人去看数据。
	ErrNeedsHuman = errors.New("需要人工处理，执行器已停下")
	// ErrPlanHasProblems 计划里有串号 / 非法引用。执行器必须整批停下，不许跳过——
	// 跳过一条串号键，等于把一个安全隐患留在库里且没人知道它没被处理。
	ErrPlanHasProblems = fmt.Errorf("%w：计划含需人工确认的引用", ErrNeedsHuman)
	// ErrStalePlan 计划生成后库被改过（或者拿错了计划文件）。
	ErrStalePlan = fmt.Errorf("%w：计划已过期", ErrNeedsHuman)
	// ErrObjectConflict 目标对象已存在但大小与源不一致（多半是上次复制到一半）。
	ErrObjectConflict = fmt.Errorf("%w：目标对象与源不一致", ErrNeedsHuman)
	// ErrPlanTampered 计划里的表名/主键/目标键与代码里的白名单或重算结果对不上。
	ErrPlanTampered = fmt.Errorf("%w：计划与代码不一致", ErrNeedsHuman)
)

// PlanFile 是执行器唯一允许的输入：磁盘上那份计划 JSON。
type PlanFile struct {
	Path string
	Hash string // 文件字节的 sha256，台账按它分批次
	Plan *Plan
}

// ReadPlan 读计划。hash 取**文件字节**而不是解析后的结构：同一份计划多一个空格
// 也应当被当成新的一次搬迁，否则两份内容不同的计划会共用台账、互相跳过。
func ReadPlan(path string) (*PlanFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取计划文件失败：%w", err)
	}
	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("计划 JSON 解析失败（是不是把人读报告当计划传进来了？）：%w", err)
	}
	sum := sha256.Sum256(raw)
	return &PlanFile{Path: path, Hash: hex.EncodeToString(sum[:]), Plan: &p}, nil
}

// lookup 是把计划里的一条引用**解析回代码常量**的结果。
//
// 计划 JSON 里带着 table_name / pk / column 这些字符串，但执行器拼 SQL 时
// 一律用 lookup.Col 里的常量：计划文件是可以被人改的，拿它拼 SQL 等于把
// 一个文本文件变成 SQL 注入面。计划里的对应字段只用来**对账**，不用来执行。
type lookup struct {
	Item  Item
	Col   Column
	Table string // 由 gorm schema 解析模型得到，不是计划里的字符串
}

func columnByLabel(label string) (Column, error) {
	var found []Column
	for _, c := range Inventory() {
		if c.Label == label {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		return Column{}, fmt.Errorf("标签 %q 在 Inventory 里匹配到 %d 条（必须恰好 1 条）", label, len(found))
	}
	return found[0], nil
}

func tableOf(c Column) (string, error) {
	s, err := schema.Parse(c.Model, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		return "", err
	}
	return s.Table, nil
}

// resolve 把一条计划条目解析成可执行的形态，同时对账计划里的溯源字段。
func resolve(it Item) (lookup, error) {
	col, err := columnByLabel(it.Column)
	if err != nil {
		return lookup{}, fmt.Errorf("%w：%v", ErrPlanTampered, err)
	}
	table, err := tableOf(col)
	if err != nil {
		return lookup{}, err
	}
	if it.Table != table || it.PK != col.PK {
		return lookup{}, fmt.Errorf("%w：第 %s=%d 条写的是 %s.%s，代码里是 %s.%s",
			ErrPlanTampered, it.PK, it.PKValue, it.Table, it.PK, table, col.PK)
	}
	return lookup{Item: it, Col: col, Table: table}, nil
}

// RefID 是台账里一条引用的身份。刻意只用**代码常量**（表 / 主键列 / 列 / 下标）
// 与主键值算，不受计划文件里那些字符串影响。
func RefID(r lookup) string {
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%d",
		r.Table, r.Col.PK, r.Item.PKValue, r.Col.Col, r.Item.Index)
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// ValidatePlan 在执行前把计划从头到尾核一遍。
//
// 最要紧的一条是**重算目标键**：计划 JSON 里的 target_key 若被改过（或由旧版本
// 生成），执行器会照着把对象搬到别人的租户前缀下，而库里也写成那个键——
// 症状是某家的图出现在另一家名片页上，两端都没报错。所以这里只认
// ObjectKeyFor(company_id, source_key) 算出来的那一个。
func ValidatePlan(p *Plan, requireClean bool) error {
	if p == nil {
		return fmt.Errorf("%w：计划为空", ErrPlanTampered)
	}
	total := 0
	for _, n := range p.Counts {
		total += n
	}
	if total != len(p.Items) {
		return fmt.Errorf("%w：Counts 合计 %d 与 Items 条数 %d 不符（计划可能被截断或改过）",
			ErrPlanTampered, total, len(p.Items))
	}
	if requireClean && len(p.Problems()) > 0 {
		return fmt.Errorf("%w：共 %d 条串号 / 非法引用", ErrPlanHasProblems, len(p.Problems()))
	}
	for _, it := range p.Items {
		r, err := resolve(it)
		if err != nil {
			return err
		}
		if it.Class != ClassNeedsMigrate {
			continue
		}
		if it.Index >= 0 && r.Col.Kind != Array {
			return fmt.Errorf("%w：%s 不是数组列，条目却带下标 %d", ErrPlanTampered, it.Column, it.Index)
		}
		if it.Index < 0 && r.Col.Kind == Array {
			return fmt.Errorf("%w：%s 是数组列，条目却缺下标", ErrPlanTampered, it.Column)
		}
		want, err := storage.ObjectKeyFor(storage.ScopeTenant, it.CompanyID, it.SourceKey)
		if err != nil {
			return fmt.Errorf("%w：company_id=%d 源键 %q 推不出目标键：%v",
				ErrPlanTampered, it.CompanyID, it.SourceKey, err)
		}
		if want != it.TargetKey {
			return fmt.Errorf("%w：company_id=%d 源键 %q 的目标键应是 %q，计划里是 %q",
				ErrPlanTampered, it.CompanyID, it.SourceKey, want, it.TargetKey)
		}
	}
	return nil
}

// MigrateLookups 返回计划里全部待搬引用，已按 Inventory 解析。
func MigrateLookups(p *Plan) ([]lookup, error) {
	out := make([]lookup, 0, len(p.Items))
	for _, it := range p.Items {
		if it.Class != ClassNeedsMigrate {
			continue
		}
		r, err := resolve(it)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// ---- 对象存储（只增不删）----------------------------------------------------

// ObjectInfo 是对象存储能给出的最小真相。
type ObjectInfo struct {
	Exists bool
	Size   int64
}

// ObjectStore 是「按对象键读写」的最小接口。
//
// 为什么不用现有的 storage.Uploader：那个接口只有 Save / SaveBytes（接收multipart
// 或字节），搬迁要的是「按裸键读一个可能很大的对象并流式写到另一个桶」，
// 全读进内存会在视频上炸。这里只暴露 Stat / Open / Put 三个动作，
// 本地盘与 S3 都能实现（S3 侧接 minio.Client 的 GetObject/PutObject/StatObject）。
type ObjectStore interface {
	Stat(key string) (ObjectInfo, error)
	Open(key string) (io.ReadCloser, error)
	// Put 必须**原子落盘**：写完才允许 Stat 看见。否则复制到一半中断，
	// 下次重跑看到的是一个存在但短一截的对象，会被当成「已复制」而跳过。
	Put(key string, r io.Reader, size int64, contentType string) error
}

// Stores 把「平台侧源」与「按租户给目标」组装起来。
//
// TargetFor 返回一个**该租户自己的**存储。它绝不允许在配置缺失时回退到平台盘：
// 那样对象会被复制到平台盘的新键下、库也被改成 t/<cid>/...，后台显示搬迁成功，
// 等真把流量切到客户桶才发现那边一个对象都没有——最典型的静默降级。
type Stores struct {
	Source    ObjectStore
	TargetFor func(companyID int64) (ObjectStore, error)

	cache map[int64]ObjectStore
}

func (s *Stores) target(cid int64) (ObjectStore, error) {
	if cid <= 0 {
		return nil, fmt.Errorf("company_id=%d 不是合法租户", cid)
	}
	if s.cache == nil {
		s.cache = map[int64]ObjectStore{}
	}
	if st, ok := s.cache[cid]; ok {
		return st, nil
	}
	if s.TargetFor == nil {
		return nil, fmt.Errorf("没有配置目标存储")
	}
	st, err := s.TargetFor(cid)
	if err != nil {
		return nil, err
	}
	s.cache[cid] = st
	return st, nil
}

// Copy 把源对象按字节复制到目标键。源不存在必须报错，不许当成「已搬好」。
func (s *Stores) Copy(srcKey, dstKey string, companyID int64) error {
	if s.Source == nil {
		return fmt.Errorf("没有配置源存储")
	}
	info, err := s.Source.Stat(srcKey)
	if err != nil {
		return fmt.Errorf("检查源对象 %q 失败：%w", srcKey, err)
	}
	if !info.Exists {
		return fmt.Errorf("源对象 %q 不存在", srcKey)
	}
	dst, err := s.target(companyID)
	if err != nil {
		return err
	}
	rc, err := s.Source.Open(srcKey)
	if err != nil {
		return fmt.Errorf("打开源对象 %q 失败：%w", srcKey, err)
	}
	defer func() { _ = rc.Close() }()
	ct := mime.TypeByExtension(filepath.Ext(dstKey))
	return dst.Put(dstKey, rc, info.Size, ct)
}

// validateObjectKey 是所有 ObjectStore 实现共用的对象键守卫。
//
// 键来自计划文件：ValidatePlan 已用 ObjectKeyFor 重算过**目标键**，但源键仍是
// 文件里的字符串，而且 S3 侧一旦把 ".." 当对象键发出去，有些网关会做路径归一。
// 本地盘与 S3 两个实现都必须过这一关，只在一个里挡等于给另一条路留洞。
func validateObjectKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") || strings.Contains(key, "\x00") {
		return fmt.Errorf("非法对象键 %q", key)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("对象键含空段或路径穿越 %q", key)
		}
	}
	return nil
}

// LocalStore 是本地盘实现（平台 local 驱动 / 单测 / 离线演练）。
//
// 键到路径这一步必须自己挡穿越：对象键来自计划文件，虽然 ValidatePlan 已经用
// ObjectKeyFor 重算过目标键、挡过 .. 与空段，但源键仍是文件里的字符串。
// 路径守卫放在这里，任何调用方都绕不过去。
type LocalStore struct{ Dir string }

func NewLocalStore(dir string) (*LocalStore, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	return &LocalStore{Dir: abs}, nil
}

func (s *LocalStore) path(key string) (string, error) {
	if err := validateObjectKey(key); err != nil {
		return "", err
	}
	p := filepath.Join(s.Dir, filepath.FromSlash(key))
	rel, err := filepath.Rel(s.Dir, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("对象键逃出了根目录 %q", key)
	}
	return p, nil
}

func (s *LocalStore) Stat(key string) (ObjectInfo, error) {
	p, err := s.path(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	fi, err := os.Stat(p)
	if os.IsNotExist(err) {
		return ObjectInfo{}, nil
	}
	if err != nil {
		return ObjectInfo{}, err
	}
	if !fi.Mode().IsRegular() {
		return ObjectInfo{}, fmt.Errorf("对象 %q 不是普通文件", key)
	}
	return ObjectInfo{Exists: true, Size: fi.Size()}, nil
}

func (s *LocalStore) Open(key string) (io.ReadCloser, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func (s *LocalStore) Put(key string, r io.Reader, size int64, _ string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".backfill-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // 成功时已被 rename 走
	n, err := io.Copy(tmp, r)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if n != size {
		_ = tmp.Close()
		return fmt.Errorf("复制 %q 短读：期望 %d 字节，实际 %d", key, size, n)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}
