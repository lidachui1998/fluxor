package subscription

import (
	"encoding/json"
	"fluxor/internal/config"
	"fluxor/internal/configgen"
	"fluxor/internal/core"
	"fluxor/internal/httpx"
	"fluxor/internal/logx"
	"net/http"
	"os"
	"path/filepath"
)

// HandleGenerateConfig 处理 POST /subscribe/generate：
// 保存配置、按当前模式生成或复制 config.yaml，并热重载内核。
func HandleGenerateConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var cfg config.SubscribeConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, "无效的请求格式: "+err.Error())
		return
	}

	if cfg.MetaBackendURL != "" && !httpx.BackendURLRegex.MatchString(cfg.MetaBackendURL) {
		httpx.WriteJSONError(w, http.StatusBadRequest, "外部面板后端地址格式不正确")
		return
	}

	// 订阅名会用作节点文件名与 provider 键，必须在此拦截非法字符
	if err := config.ValidateSubscriptionNames(cfg.Subscriptions); err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 物理清理标记删除的配置文件，使用 filepath.Base 防范路径穿越
	if len(cfg.DeletePhysical) > 0 {
		for _, name := range cfg.DeletePhysical {
			// 与写入侧使用同一套文件名规则，避免删错文件或漏删
			fileName := config.SanitizeSubscriptionFileName(name)
			if fileName == ".yaml" {
				continue
			}
			targetFile := filepath.Join(config.CoreWorkDir, "proxies", fileName)
			if _, err := os.Stat(targetFile); err == nil {
				if err := os.Remove(targetFile); err != nil {
					logx.Error(logx.ModuleSub, "physical delete of subscription file %s failed: %v", targetFile, err)
				} else {
					logx.Info(logx.ModuleSub, "subscription file physically deleted: %s", targetFile)
				}
			}
		}
	}
	cfg.DeletePhysical = nil // 清空临时字段避免持久化

	// 自定义规则与流量隧道归各自的专用接口维护（切换模式按订阅、融合模式按规则集档位、
	// 自定义模式独立一份）：本接口只负责生成配置文件，不能在请求体缺少这些字段时把它们
	// 清空——否则 GenerateConfig 会生成一份不含自定义规则/隧道的 config.yaml，且内存态被
	// 清空后，下一次编辑会把「只剩本次编辑」的列表写回文件。详见 config.AdoptServerOwnedFields。
	config.Mu.RLock()
	prev := config.Current
	config.Mu.RUnlock()
	cfg.AdoptServerOwnedFields(prev)

	// 自定义模式：不使用订阅，配置由模板 + 手工节点 + 标准规则集生成
	if cfg.Mode == config.ModeCustom {
		generateCustomConfig(w, cfg)
		return
	}

	// 切换模式
	if cfg.Mode == "switch" {
		// 如果订阅列表为空，生成基础配置，清除选中状态，保存并重载
		if len(cfg.Subscriptions) == 0 {
			// 生成基础配置文件
			if err := configgen.GenerateBaseConfig(cfg); err != nil {
				httpx.WriteJSONError(w, http.StatusInternalServerError, "生成基础配置失败: "+err.Error())
				return
			}
			// 清除选中的订阅
			cfg.ActiveSubscription = ""
			// 保存配置到全局并持久化
			config.Mu.Lock()
			config.Current = cfg
			config.Mu.Unlock()
			if err := config.SaveSubscribeConfig(); err != nil {
				logx.Error(logx.ModuleSub, "saving subscription config failed: mode=switch subscription=none err=%v", err)
			}
			// 重置定时器（无订阅时需停止所有定时器）
			StopAllTimers()
			StartAllTimers() // 会检查模式，切换模式且无订阅时会跳过启动
			// 重载内核
			if err := core.ReloadCore(); err != nil {
				httpx.RespondJSON(w, http.StatusOK, map[string]string{
					"status":  "warning",
					"message": "基础配置已生成，但重载内核失败: " + err.Error(),
				})
				return
			}
			httpx.RespondJSON(w, http.StatusOK, map[string]string{
				"status":  "ok",
				"message": "已清除订阅，切换到基础配置",
			})
			return
		}

		// 有订阅时，确保所有订阅文件已下载
		if err := ensureSubscriptionFiles(&cfg); err != nil {
			httpx.WriteJSONError(w, http.StatusInternalServerError, "下载订阅文件失败: "+err.Error())
			return
		}
		// 检查是否选中了订阅
		if cfg.ActiveSubscription == "" {
			httpx.WriteJSONError(w, http.StatusBadRequest, "切换模式下请先选择一个订阅")
			return
		}
		// 选中名必须是当前订阅列表中的真实成员。
		// active_subscription 以订阅名为键，改名/删除后客户端可能仍持有旧名；
		// 只校验空字符串会让旧名一路走到下面，按旧名复制旧订阅文件，
		// 出现「界面显示新名字、实际生效旧配置」的静默错配。
		if !activeSubscriptionExists(cfg) {
			httpx.WriteJSONError(w, http.StatusBadRequest, "选中的订阅不存在: "+cfg.ActiveSubscription)
			return
		}
		// 构建源文件路径
		srcFile := filepath.Join(config.CoreWorkDir, "proxies", config.SanitizeSubscriptionFileName(cfg.ActiveSubscription))
		if _, err := os.Stat(srcFile); err != nil {
			httpx.WriteJSONError(w, http.StatusInternalServerError, "选中的订阅文件不存在: "+err.Error())
			return
		}
		// 复制文件到 configTarget，并叠加该订阅的自定义规则与流量隧道
		if _, err := writeRuntimeConfig(cfg.ActiveSubscription,
			copyCustomRules(cfg, cfg.ActiveSubscription),
			copyCustomTunnels(cfg, cfg.ActiveSubscription)); err != nil {
			httpx.WriteJSONError(w, http.StatusInternalServerError, "生成运行配置失败: "+err.Error())
			return
		}
		// 保存配置到 subscribe.json
		config.Mu.Lock()
		config.Current = cfg
		config.Mu.Unlock()
		if err := config.SaveSubscribeConfig(); err != nil {
			logx.Error(logx.ModuleSub, "saving subscription config failed: mode=switch err=%v", err)
		}
		// 重置定时器
		StopAllTimers()
		StartAllTimers()
		// 重载内核
		if err := core.ReloadCore(); err != nil {
			httpx.RespondJSON(w, http.StatusOK, map[string]string{
				"status":  "warning",
				"message": "配置文件已复制，但重载内核失败: " + err.Error(),
			})
			return
		}
		httpx.RespondJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"message": "已切换到订阅 " + cfg.ActiveSubscription + " 的配置",
		})
		return
	}

	// ---------- 融合模式（原有逻辑） ----------
	// 不再预先删除旧 config.yaml：GenerateConfig 会在同一路径上直接生成并覆盖
	// （writeConfigTarget 用 os.WriteFile 整份覆写，且从不读取旧文件）。
	// 预删除只会留出一个「文件不存在」的窗口：若随后生成失败，内核热重载或
	// 重启就会因缺少配置而失败——此前的实现正是如此。

	config.Mu.Lock()
	config.Current = cfg
	config.Mu.Unlock()
	if err := config.SaveSubscribeConfig(); err != nil {
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存配置失败: "+err.Error())
		return
	}
	// 重置定时器
	StopAllTimers()
	StartAllTimers()

	if err := configgen.GenerateConfig(cfg); err != nil {
		httpx.WriteJSONError(w, http.StatusInternalServerError, "生成配置文件失败: "+err.Error())
		return
	}

	if cfg.MetaBackendURL != "" {
		if err := modifyMetaConfig(cfg.MetaBackendURL); err != nil {
			logx.Warn(logx.ModuleSub, "updating MetaCubeXD backend URL failed: %v", err)
		}
	}

	if err := core.ReloadCore(); err != nil {
		httpx.RespondJSON(w, http.StatusOK, map[string]string{
			"status":  "warning",
			"message": "配置文件已生成，但重载内核失败: " + err.Error(),
		})
		return
	}

	updateAllSubscriptionsMetadata(&cfg)
	// 将更新后的 cfg 保存到全局并持久化
	config.Mu.Lock()
	config.Current = cfg
	config.Mu.Unlock()
	if err := config.SaveSubscribeConfig(); err != nil {
		logx.Error(logx.ModuleSub, "saving subscription config failed: mode=merge err=%v", err)
	}

	httpx.RespondJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"message": "配置文件已生成并成功重载内核",
	})
}
