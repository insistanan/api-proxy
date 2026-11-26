package config

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestUpdateChannel_RejectsMismatchedIdentityWithoutWriting(t *testing.T) {
	for _, kind := range allChannelKinds {
		t.Run(kind, func(t *testing.T) {
			cm := newTransactionTestManager(t, kind)
			before := cm.GetConfig()
			disk, err := os.ReadFile(cm.configFile)
			if err != nil {
				t.Fatal(err)
			}
			expected, name := "second", "must-not-be-written"
			_, err = cm.UpdateChannel(kind, 0, UpstreamUpdate{ExpectedChannelID: &expected, Name: &name})
			if !errors.Is(err, ErrChannelIdentityConflict) {
				t.Fatalf("身份错位应返回冲突，得到 %v", err)
			}
			afterDisk, err := os.ReadFile(cm.configFile)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, cm.GetConfig()) || !bytes.Equal(disk, afterDisk) {
				t.Fatal("身份冲突不得修改配置或磁盘")
			}
			expected = "first"
			if _, err := cm.UpdateChannel(kind, 0, UpstreamUpdate{ExpectedChannelID: &expected, Name: &name}); err != nil {
				t.Fatalf("身份一致时应允许更新: %v", err)
			}
		})
	}
}
