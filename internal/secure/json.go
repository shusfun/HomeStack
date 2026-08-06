package secure

import (
	"encoding/json"
	"fmt"
)

func jsonMarshal(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("编码封装载荷失败: %w", err)
	}
	return data, nil
}

func jsonUnmarshal(data []byte, target any) error {
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("解析封装载荷失败: %w", err)
	}
	return nil
}
