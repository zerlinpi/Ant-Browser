package automation

import (
	"reflect"
	"testing"
)

func TestTaskEnvironmentDoesNotExposeCloudDeviceCredential(t *testing.T) {
	source := []string{"PATH=runtime", "ANT_CLOUD_DEVICE_CREDENTIAL=secret", "ant_cloud_device_credential=other", "USER_SETTING=value"}
	want := []string{"PATH=runtime", "USER_SETTING=value"}
	if got := taskEnvironment(source); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected task environment: %v", got)
	}
	if len(source) != 4 {
		t.Fatal("source environment mutated")
	}
}
