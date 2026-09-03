package popup

import (
	"testing"
	"time"
)

func TestPopupShow(t *testing.T) {
	Show("测试提醒内容")
	time.Sleep(100 * time.Millisecond)
	Shutdown()
}
