package model

// StorageOwner 回答「这家企业的对象此刻由谁接管」。
//
// 唯一真值源是 storage_profiles + storage_bindings（迁移 0054）；旧表
// tenant_storage_configs 已随 0055 删除。「这家到底写进哪个桶」只能有一个答案：
// 配额判定、后台状态提示、公网 origin 都必须按同一个答案走，否则会出现
// 「上传已经进客户桶、平台配额却还在拦」这种自相矛盾的状态。
//
// 由 storageprofile.Resolver.OwnerOf 产生，handler 只消费不构造：
// 构造点只允许在解析器里，这样「谁在写对象」永远只有一个裁判。
type StorageOwner struct {
	// Driver 是生效存储的驱动（local / minio / oss / s3）。
	Driver string
	// ProfileID / ProfileName 供后台把「被哪套档案接管」说清楚。
	// 走平台自身 / 全局默认时 ProfileID 为 0。
	ProfileID   int64
	ProfileName string
}

// UsesOwnBucket 回报「对象是否落在客户自己的桶里」。
// local 驱动（含内置本地档案）不算：对象仍在平台盘上，平台配额与平台域名仍然适用。
func (o StorageOwner) UsesOwnBucket() bool {
	return o.Driver != "" && o.Driver != "local"
}
