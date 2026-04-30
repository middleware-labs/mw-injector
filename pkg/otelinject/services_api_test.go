package otelinject

import (
	"testing"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

func TestBuildInstancesFromSetting(t *testing.T) {
	t.Run("uses pre-aggregated Instances when populated", func(t *testing.T) {
		setting := discovery.ServiceSetting{
			PID:   100,
			Owner: "root",
			Status: "running",
			Instances: []discovery.ReportInstanceInfo{
				{PID: 100, Owner: "root", Status: "running"},
				{PID: 200, Owner: "deploy", Status: "running"},
			},
		}

		var instances []InstanceInfo
		if len(setting.Instances) > 0 {
			for _, ri := range setting.Instances {
				instances = append(instances, InstanceInfo{
					PID:    ri.PID,
					Owner:  ri.Owner,
					Status: ri.Status,
				})
			}
		} else {
			instances = append(instances, InstanceInfo{
				PID:    setting.PID,
				Owner:  setting.Owner,
				Status: setting.Status,
			})
		}

		if len(instances) != 2 {
			t.Fatalf("got %d instances, want 2", len(instances))
		}
		if instances[0].PID != 100 || instances[1].PID != 200 {
			t.Errorf("PIDs = [%d, %d], want [100, 200]", instances[0].PID, instances[1].PID)
		}
	})

	t.Run("falls back to top-level fields when Instances nil", func(t *testing.T) {
		setting := discovery.ServiceSetting{
			PID:    300,
			Owner:  "app-user",
			Status: "sleeping",
		}

		var instances []InstanceInfo
		if len(setting.Instances) > 0 {
			for _, ri := range setting.Instances {
				instances = append(instances, InstanceInfo{
					PID:    ri.PID,
					Owner:  ri.Owner,
					Status: ri.Status,
				})
			}
		} else {
			instances = append(instances, InstanceInfo{
				PID:    setting.PID,
				Owner:  setting.Owner,
				Status: setting.Status,
			})
		}

		if len(instances) != 1 {
			t.Fatalf("got %d instances, want 1", len(instances))
		}
		if instances[0].PID != 300 {
			t.Errorf("PID = %d, want 300", instances[0].PID)
		}
		if instances[0].Owner != "app-user" {
			t.Errorf("Owner = %q, want %q", instances[0].Owner, "app-user")
		}
	})
}
