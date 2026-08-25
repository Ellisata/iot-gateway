package mqtt

import (
	"encoding/json"
	"fmt"

	"iot-gateway/collector"
)

// pushPoint 推送到 MQTT 的单条点位数据
type pushPoint struct {
	DeviceID          string `json:"deviceId"`
	DeviceName        string `json:"deviceName"`
	DeviceAddressID   string `json:"deviceAddressId"`
	DeviceAddressName string `json:"deviceAddressName"`
	Value             string `json:"value"`
	DataType          string `json:"dataType"`
	Kind              string `json:"kind"`
	Protocol          string `json:"protocol"`
	Quality           int    `json:"quality"`
	CollectedAt       string `json:"collectedAt"`
}

// marshalBatch 将同一设备的采集记录序列化为 JSON 数组
func marshalBatch(records []collector.CollectedRecord, now string) ([]byte, error) {
	points := make([]pushPoint, 0, len(records))
	for _, r := range records {
		points = append(points, pushPoint{
			DeviceID:          r.DeviceID,
			DeviceName:        r.DeviceName,
			DeviceAddressID:   r.DeviceAddressID,
			DeviceAddressName: r.DeviceAddressName,
			Value:             r.Value,
			DataType:          r.DataType,
			Kind:              r.Kind,
			Protocol:          r.Protocol,
			Quality:           r.Quality,
			CollectedAt:       now,
		})
	}
	payload, err := json.Marshal(points)
	if err != nil {
		return nil, fmt.Errorf("mqtt: marshal batch failed: %w", err)
	}
	return payload, nil
}
