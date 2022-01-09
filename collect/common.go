package collect

import (
	"sync"

	"github.com/jyothri/hdd/db"
)

var ParseInfo []db.FileData
var lock sync.RWMutex

func checkError(err error) {
	if err != nil {
		panic(err)
	}
}
