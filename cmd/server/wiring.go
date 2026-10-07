package main

import (
	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
	"github.com/linguo2625469/workbuddy2api-panel/internal/server"
)

// realmAwareAvailableForModel 构造会话粘性路由按模型可用口径的 realm 感知闭包。
//
// 粘性分配的模型名可能带 realm 前缀（"global:gpt-5.4" / "cn:glm-5.2"）：必须按前缀剥出
// realm + bareModel，再交给分池选号域过滤——否则裸名取池子全集，global 号会被粘性分配给
// CN 前缀请求（跨 realm 泄漏）。
//
// **必须与 handler 用同一个 resolver**：裸名的默认域由 model_default_realm 策略
// （cn/global/auto）决定，两处不一致会导致粘性分配域与请求实际路由域错位。
func realmAwareAvailableForModel(p *pool.Pool, rr *server.RealmResolver) func(model string) []string {
	return func(model string) []string {
		realm, bare := rr.Resolve(model)
		return p.WeightedAvailableUIDsForModelRealm(bare, realm)
	}
}
