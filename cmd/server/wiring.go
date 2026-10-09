package main

import (
	"workbuddy_manager/internal/pool"
	"workbuddy_manager/internal/server"
)

// realmAwareAvailableForModel 构造会话粘性路由按模型可用口径的 realm 感知闭包。
//
// 粘性分配的模型名可能带 realm 前缀（"global:gpt-5.4" / "cn:glm-5.2"）：必须按前缀剥出
// realm + bareModel，再交给分池选号域过滤——否则裸名取池子全集，global 号会被粘性分配给
// CN 前缀请求（跨 realm 泄漏）。
//
// **必须与 handler 用同一个 resolver**：裸名的默认域由 model_default_realm 策略
// （cn/global/auto/auto:global,cn）决定，两处不一致会导致粘性分配域与请求实际路由域错位。
// 在 auto 策略下按优先级候选域检查，返回首个有可用账号的域候选集。
func realmAwareAvailableForModel(p *pool.Pool, rr *server.RealmResolver) func(model string) []string {
	return func(model string) []string {
		candidates, bare := rr.CandidateRealms(model)
		for _, realm := range candidates {
			uids := p.WeightedAvailableUIDsForModelRealm(bare, realm)
			if len(uids) > 0 {
				return uids
			}
		}
		if len(candidates) > 0 {
			return p.WeightedAvailableUIDsForModelRealm(bare, candidates[0])
		}
		return nil
	}
}
