package collect

import (
	"fmt"
	"sync"
)

var lock sync.RWMutex

func checkError(err error, msg ...string) {
	if err != nil {
		fmt.Println(msg)
		panic(err)
	}
}
