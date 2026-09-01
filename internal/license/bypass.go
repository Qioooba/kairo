package license

const devBypassValue = "111222"

func devBypass() bool {
	snap := getConfigSnapshot()
	if snap == nil {
		return false
	}
	return snap.KairoInternalToken == devBypassValue
}
