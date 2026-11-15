package semantic

// VectorModelRequiredError 由实际向量提供方返回，数据集与意图接口共享可操作诊断。
type VectorModelRequiredError struct {
	Message     string `json:"message"`
	InstallPath string `json:"installPath"`
	PresetsURL  string `json:"presetsUrl"`
}

func (e *VectorModelRequiredError) Error() string { return e.Message }
