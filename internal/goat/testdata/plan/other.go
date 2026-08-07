package plan

func NewFile(name string) File {
	return File{Name: name}
}

func useShared() string {
	return sharedHelper()
}
