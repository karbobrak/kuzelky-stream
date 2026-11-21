package google

// SetBaseURLForTest points all Google endpoints at a fake server (tests only).
func SetBaseURLForTest(base string) {
	tokenEndpoint = base + "/token"
	apiBase = base + "/youtube/v3/"
	driveBase = base + "/drive/v3/"
	uploadBase = base + "/upload/drive/v3/"
	sheetsBase = base + "/sheets/v4/"
}
