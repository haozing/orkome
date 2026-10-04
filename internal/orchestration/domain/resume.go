package domain

func EngineTagCompatible(produced, current string) bool {
	if current == "" {
		return true
	}
	return produced == "" || produced == current
}
