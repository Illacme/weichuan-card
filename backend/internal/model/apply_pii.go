package model

import (
	"log"

	"gorm.io/gorm"

	"weichuan/internal/pii"
)

// ===== OpenApply（开通申请）的 PII 加解密 =====
//
// 方案与 Employee 完全一致：明文 gorm:"-" 只在内存，落库是原列名上的密文 + 盲索引。
// 只挂 BeforeCreate / AfterFind，更新走显式 map（申请单基本只插入、改状态）。

func (a *OpenApply) BeforeCreate(tx *gorm.DB) error {
	var err error
	if a.NameEnc, err = pii.Encrypt(a.Name); err != nil {
		return err
	}
	if a.PhoneEnc, err = pii.Encrypt(a.Phone); err != nil {
		return err
	}
	if a.Phone2Enc, err = pii.Encrypt(a.Phone2); err != nil {
		return err
	}
	if a.EmailEnc, err = pii.Encrypt(a.Email); err != nil {
		return err
	}
	if a.NicknameEnc, err = pii.Encrypt(a.Nickname); err != nil {
		return err
	}
	a.NameBidx = pii.BlindIndex(a.Name)
	a.PhoneBidx = pii.BlindIndex(a.Phone)
	a.Phone2Bidx = pii.BlindIndex(a.Phone2)
	a.EmailBidx = pii.BlindIndex(a.Email)
	a.NicknameBidx = pii.BlindIndex(a.Nickname)
	return nil
}

func (a *OpenApply) AfterFind(tx *gorm.DB) error {
	dec := func(col, enc string) string {
		v, err := pii.Decrypt(enc)
		if err != nil {
			log.Printf("[PII][WARN] 开通申请 %d 的 %s 解密失败：%v", a.ID, col, err)
			return ""
		}
		return v
	}
	a.Name = dec("name", a.NameEnc)
	a.Phone = dec("phone", a.PhoneEnc)
	a.Phone2 = dec("phone2", a.Phone2Enc)
	a.Email = dec("email", a.EmailEnc)
	a.Nickname = dec("nickname", a.NicknameEnc)
	return nil
}

// ===== CardModifyApply.Snapshot（修改快照 JSON）的整段加解密 =====
//
// Snapshot 是整段 JSON（{avatar,name,title,phone,landline,email}），含 PII。
// 整段作为一个密文落库：不逐字段拆（JSON 里拆字段要保持结构一致性代价更高），
// 也不需要盲索引（申请列表按员工姓名检索走 Employee 子查询，不查快照内容）。

func (a *CardModifyApply) BeforeCreate(tx *gorm.DB) error {
	enc, err := pii.Encrypt(a.Snapshot)
	if err != nil {
		return err
	}
	a.SnapshotEnc = enc
	return nil
}

func (a *CardModifyApply) AfterFind(tx *gorm.DB) error {
	v, err := pii.Decrypt(a.SnapshotEnc)
	if err != nil {
		log.Printf("[PII][WARN] 名片修改申请 %d 的 snapshot 解密失败：%v", a.ID, err)
		v = ""
	}
	a.Snapshot = v
	return nil
}

// ===== 存量回填 =====

// BackfillPII 统一回填三处 PII：员工 / 开通申请 / 名片修改快照。幂等，
// 只处理「非空且未加密」或「已加密但缺盲索引」的字段。启动时调用。
func BackfillPII(db *gorm.DB) error {
	if err := BackfillEmployees(db); err != nil {
		return err
	}
	if err := BackfillOpenApplies(db); err != nil {
		return err
	}
	return BackfillModifySnapshots(db)
}

// BackfillOpenApplies 回填开通申请单的 PII 明文（字段集合与员工表一致）。
func BackfillOpenApplies(db *gorm.DB) error {
	if !db.Migrator().HasTable("open_applies") {
		return nil
	}
	var rows []struct {
		ID           int64
		Name         string
		Phone        string
		Phone2       string
		Email        string
		Nickname     string
		NameBidx     string
		PhoneBidx    string
		Phone2Bidx   string
		EmailBidx    string
		NicknameBidx string
	}
	if err := db.Table("open_applies").
		Select("id, name, phone, phone2, email, nickname, " +
			"name_bidx, phone_bidx, phone2_bidx, email_bidx, nickname_bidx").
		Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		upd := map[string]any{}
		if err := backfillField(upd, "name", r.Name, r.NameBidx); err != nil {
			return err
		}
		if err := backfillField(upd, "phone", r.Phone, r.PhoneBidx); err != nil {
			return err
		}
		if err := backfillField(upd, "phone2", r.Phone2, r.Phone2Bidx); err != nil {
			return err
		}
		if err := backfillField(upd, "email", r.Email, r.EmailBidx); err != nil {
			return err
		}
		if err := backfillField(upd, "nickname", r.Nickname, r.NicknameBidx); err != nil {
			return err
		}
		if len(upd) == 0 {
			continue
		}
		if err := db.Table("open_applies").Where("id = ?", r.ID).Updates(upd).Error; err != nil {
			return err
		}
	}
	return nil
}

// BackfillModifySnapshots 回填名片修改申请的 Snapshot（整段加密，无盲索引）。
func BackfillModifySnapshots(db *gorm.DB) error {
	if !db.Migrator().HasTable("card_modify_applies") {
		return nil
	}
	var rows []struct {
		ID       int64
		Snapshot string
	}
	if err := db.Table("card_modify_applies").Select("id, snapshot").Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		if r.Snapshot == "" || pii.IsEncrypted(r.Snapshot) {
			continue
		}
		enc, err := pii.Encrypt(r.Snapshot)
		if err != nil {
			return err
		}
		if err := db.Table("card_modify_applies").
			Where("id = ?", r.ID).Updates(map[string]any{"snapshot": enc}).Error; err != nil {
			return err
		}
	}
	return nil
}
