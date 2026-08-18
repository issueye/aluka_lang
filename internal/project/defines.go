package project

// ProductionDefines 是 web production 构建注入的最小 define 集。
func ProductionDefines() map[string]string {
	return map[string]string{
		"process.env.NODE_ENV":                    `"production"`,
		"__VUE_OPTIONS_API__":                     "true",
		"__VUE_PROD_DEVTOOLS__":                   "false",
		"__VUE_PROD_HYDRATION_MISMATCH_DETAILS__": "false",
	}
}

// DevelopmentDefines 是 web development / aluka dev 注入的最小 define 集。
func DevelopmentDefines() map[string]string {
	return map[string]string{
		"process.env.NODE_ENV":                    `"development"`,
		"__VUE_OPTIONS_API__":                     "true",
		"__VUE_PROD_DEVTOOLS__":                   "true",
		"__VUE_PROD_HYDRATION_MISMATCH_DETAILS__": "true",
	}
}

// DefaultDefines 按 mode 返回内置 define；未知 mode 按 production。
func DefaultDefines(mode string) map[string]string {
	if mode == "development" {
		return DevelopmentDefines()
	}
	return ProductionDefines()
}

func mergeDefines(opts Options) map[string]string {
	_, mode := opts.BuildEnv()
	out := make(map[string]string, len(opts.Defines)+4)
	for k, v := range DefaultDefines(mode) {
		out[k] = v
	}
	// 用户 / 配置 define 覆盖内置（与 Vite/esbuild 一致）。
	for k, v := range opts.Defines {
		out[k] = v
	}
	return out
}
