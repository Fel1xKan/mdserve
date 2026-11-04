package config

// Redacted 返回一份把敏感值替换成 *** 的副本，用于 `mdserve config` 输出。
// 不直接改原对象，避免调用方误用被清空的配置。
//
// 只遮蔽"秘密本身"：口令哈希、隧道 token 文件内容所在的路径不算秘密，
// 保留可见，方便排查；`config` 从不输出文件内容。
func (c Config) Redacted() Config {
	out := c
	if out.Mounts != nil {
		mounts := make([]Mount, len(out.Mounts))
		copy(mounts, out.Mounts)
		for i := range mounts {
			if mounts[i].Auth == nil {
				continue
			}
			a := *mounts[i].Auth
			if a.PasswordHash != "" {
				a.PasswordHash = "***"
			}
			mounts[i].Auth = &a
		}
		out.Mounts = mounts
	}
	return out
}
