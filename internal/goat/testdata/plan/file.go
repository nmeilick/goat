package plan

const (
	Alpha Mode = iota
	Beta
)

type Mode int

const (
	ConstA = 1
	ConstB
)

var MultiA, MultiB = 1, 2

type File struct {
	Name string
}

func (f File) Stat() string {
	return f.Name
}

type Dir struct {
	Path string
}

func (d Dir) Stat() string {
	return d.Path
}

func FileModify(f File) File {
	return File{Name: f.Name + "!"}
}

func FileMode() Mode {
	return Alpha
}

func init() {}

func Top() string {
	return mid() + sharedHelper() + testHelper()
}

func mid() string {
	return leaf()
}

func leaf() string {
	return config{}.name
}

type config struct {
	name string
}

func sharedHelper() string {
	return "shared"
}

func testHelper() string {
	return "test"
}

func deadCode() string {
	return "dead"
}
