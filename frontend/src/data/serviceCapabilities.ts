import { useEffect, useState } from 'react';
import { useAuth } from '../auth';
import { requestJson } from './http';
import { asArray, asBoolean, asRecord, asString } from './guards';
import { ApiError, describeError } from './errors';
import { isApiMode } from './sources';

export type FeatureName = 'media_prepare' | 'visual_analysis' | 'audio_report' | 'report_generation' | 'live_capture';
export interface FeatureStatus { available: boolean; reasons: string[] }
export interface ServiceCapabilities {
  checked_at: string;
  media_prepare: FeatureStatus;
  visual_analysis: FeatureStatus;
  audio_report: FeatureStatus;
  report_generation: FeatureStatus;
  live_capture: FeatureStatus;
}
export function parseServiceCapabilities(value: unknown): ServiceCapabilities {
  const source = asRecord(value, 'ServiceCapabilities');
  const feature = (key: FeatureName): FeatureStatus => {
    const row = asRecord(source[key], key);
    const available = asBoolean(row, 'available', key);
    const reasons = asArray(row['reasons'], `${key}.reasons`).map((item, index) => {
      if (typeof item !== 'string') throw new Error(`${key}.reasons[${index}] must be text`);
      return item;
    });
    if (available !== (reasons.length === 0)) throw new Error(`${key}: inconsistent availability`);
    return { available, reasons };
  };
  const checked_at = asString(source, 'checked_at', 'ServiceCapabilities');
  if (!Number.isFinite(Date.parse(checked_at))) throw new Error('Invalid capability check time');
  return { checked_at, media_prepare: feature('media_prepare'), visual_analysis: feature('visual_analysis'), audio_report: feature('audio_report'), report_generation: feature('report_generation'), live_capture: feature('live_capture') };
}
export async function getServiceCapabilities(): Promise<ServiceCapabilities> {
  const { data } = await requestJson<unknown>('/service-capabilities');
  try { return parseServiceCapabilities(data); }
  catch (error) { throw new ApiError({ code: 'MALFORMED_RESPONSE', status: 200, message: error instanceof Error ? error.message : 'Invalid service capabilities' }); }
}
export const capabilityReason: Record<string, string> = {
  MEDIA_WORKER_UNAVAILABLE: '录像处理服务尚未就绪，请联系系统管理员启动或检查处理服务。',
  VISION_NOT_CONFIGURED: '尚未启用视觉分析，请联系系统管理员配置。',
  VISUAL_WORKER_UNAVAILABLE: '视觉处理服务或固定模型尚未就绪，请联系系统管理员检查模型并重启处理服务。',
  ASR_NOT_CONFIGURED: '尚未配置语音识别，请联系系统管理员配置语音模型。',
  REPORT_NOT_CONFIGURED: '尚未配置报告模型，请联系系统管理员配置。',
  AUDIO_WORKER_UNAVAILABLE: '音频或关键帧处理服务尚未就绪，请联系系统管理员检查。',
  BUDGET_UNAVAILABLE: '模型可用预算不足，或账目待核对，请联系系统管理员处理。',
  CAPTURE_NOT_CONFIGURED: '尚未启用手机采集，请联系系统管理员配置可信 HTTPS 和采集服务。',
};
export function featureProblem(feature: FeatureStatus): string {
  return feature.reasons.map(reason => capabilityReason[reason] ?? '此功能暂未就绪，请联系系统管理员检查。').join(' ');
}
export function useServiceCapabilities(enabled = true) {
  const { sessionKey } = useAuth();
  const [state, setState] = useState<{ key: string; data: ServiceCapabilities | null; error: string | null }>({ key: sessionKey, data: null, error: null });
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!enabled || !isApiMode()) return;
    let active = true, stopped = false, inFlight = false;
    setState({ key: sessionKey, data: null, error: null });
    const read = async () => {
      if (stopped || inFlight) return;
      inFlight = true;
      try {
        const data = await getServiceCapabilities();
        if (active) setState({ key: sessionKey, data, error: null });
      } catch (error) {
        if (error instanceof ApiError && [401,403,410].includes(error.status)) stopped = true;
        if (active) setState({ key: sessionKey, data: null, error: describeError(error) });
      } finally {
        inFlight = false;
      }
    };
    void read();
    const timer = window.setInterval(() => { if (document.visibilityState !== 'hidden') void read(); }, 15000);
    return () => { active = false; window.clearInterval(timer); };
  }, [enabled, sessionKey, revision]);
  return { data: state.key === sessionKey ? state.data : null, error: state.key === sessionKey ? state.error : null, reload: () => setRevision(value => value + 1) };
}
