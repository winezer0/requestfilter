package requestfilter

import "testing"

// TestBodylessGETParticipatesInCoverage 回归：GET/HEAD/OPTIONS 无 body
// 时视为 BodyCaptured，否则参数状态永远 Partial，覆盖判定与路径
// 泛化整体静默失效（爬虫场景踩过的坑）。
func TestBodylessGETParticipatesInCoverage(t *testing.T) {
	session, err := NewSession(Options{
		DedupStrategy:        DedupStrategyCoverageParameters,
		PathSegmentThreshold: 2,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	decide := func(url string) Action {
		decision, reservation, err := session.Reserve(Request{Method: "GET", URL: url})
		if err != nil {
			t.Fatalf("Reserve %s: %v", url, err)
		}
		if decision.Action == ActionAllow {
			if err := session.Commit(reservation); err != nil {
				t.Fatalf("Commit: %v", err)
			}
		}
		return decision.Action
	}

	// 同参数重复应被覆盖过滤（修复前 Partial 全放行）
	if decide("https://x/items?id=1") != ActionAllow {
		t.Fatal("首个请求应放行")
	}
	if decide("https://x/items?id=2") != ActionDrop {
		t.Error("同端点同参数位置应被覆盖过滤")
	}

	// 路径泛化应生效：同位置累计阈值个不同值后，新变体被过滤
	for i := 0; i < 2; i++ {
		if decide("https://x/users/"+string(rune('a'+i))) != ActionAllow {
			t.Fatalf("泛化阈值内应放行，第 %d 个", i)
		}
	}
	if decide("https://x/users/z") != ActionDrop {
		t.Error("泛化后同形状 URL 应被过滤")
	}

	// 带 body 的 POST 不受影响：BodyCaptured=false 仍按 Partial 处理
	s2, err := NewSession(Options{DedupStrategy: DedupStrategyCoverageParameters})
	if err != nil {
		t.Fatalf("NewSession s2: %v", err)
	}
	defer s2.Close()
	for i := 0; i < 2; i++ {
		d, _, err := s2.Reserve(Request{Method: "POST", URL: "https://x/login"})
		if err != nil {
			t.Fatalf("Reserve POST: %v", err)
		}
		if d.ParameterStatus != ParameterStatusPartial {
			t.Errorf("未标记 BodyCaptured 的 POST 应保持 Partial，得到 %s", d.ParameterStatus)
		}
		if d.Action != ActionAllow {
			t.Errorf("Partial 请求应放行不记录，得到 %s", d.Action)
		}
	}
}
