package fake

import (
	"fmt"
	"testing"
	"time"

	"iot-gateway/driver"
	_ "iot-gateway/driver/rockwell/cip"
	"iot-gateway/model/po"
)

func TestLatencyDriverProbe(t *testing.T) {
	srv, err := NewCIPABWithLatency(1 * time.Millisecond)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer srv.Close()

	d, err := driver.Create("Rockwell.CIP")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.Connect(fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, srv.Port())); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer d.Close()

	// 125 点 = 恰好 1 帧；5000 点 = 40 帧
	for _, n := range []int{125, 250, 500} {
		addrs := make([]po.DeviceAddress, 0, n)
		for i := 0; i < n; i++ {
			addrs = append(addrs, po.DeviceAddress{
				ID: fmt.Sprintf("a%d", i), Name: fmt.Sprintf("Arr[%d]", i), DataType: "int16",
			})
		}
		if _, err := d.Read(addrs); err != nil {
			t.Fatalf("warmup %d: %v", n, err)
		}
		start := time.Now()
		if _, err := d.Read(addrs); err != nil {
			t.Fatalf("read %d: %v", n, err)
		}
		t.Logf("read %4d pts (1/2/4 frames): %v", n, time.Since(start))
	}
}
