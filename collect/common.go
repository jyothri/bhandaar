package collect

import (
	"sync"
)

var lock sync.RWMutex

func checkError(err error) {
	if err != nil {
		panic(err)
	}
}
