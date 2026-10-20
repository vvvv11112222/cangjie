import type { ReactElement } from 'react';
import { Card } from '../components/ui';
import { IconCamera, IconChevronRight, IconReport, IconVideo } from '../components/icons';

const CONNECTION_STEPS = [
  {
    number: '01',
    title: '从课堂发起',
    text: '进入具体课堂的“实时课堂”标签创建采集，采集始终绑定当前课堂，不建立脱离课表的设备记录。',
  },
  {
    number: '02',
    title: '手机配对发布',
    text: '系统生成短时配对入口；手机获得发布授权后推送音视频，观看端使用独立的短时读取授权。',
  },
  {
    number: '03',
    title: '停止后统一分析',
    text: '停止采集后，服务端将录像转成课堂媒体，继续沿用媒体检查、转写、证据和报告复核流程。',
  },
] as const;

const FIXED_DEVICE_PLAN = [
  { number: '01', title: '按教室关联', text: '固定摄像头与教室、课表关联，录像自动归入对应课堂。' },
  { number: '02', title: '记录采集状态', text: '记录在线、断流和缺失时段，避免把缺失画面当作课堂事实。' },
  { number: '03', title: '沿用证据复核', text: '采集完成后继续使用现有分析、证据核对和报告发布流程。' },
] as const;

export function DevicesPage(): ReactElement {
  return (
    <div className="page">
      <header className="page-head">
        <div>
          <p className="eyebrow">DEVICE CONNECTION</p>
          <h1>设备接入</h1>
          <p className="muted">了解实时课堂如何接入现有课堂、录像与分析流程。</p>
        </div>
        <a className="btn btn-primary" href="#/sessions">进入课堂档案</a>
      </header>

      <Card title="实时课堂接入链路" subtitle="手机采集从具体课堂发起，停止后自动进入课堂录像与分析流程。">
        <div className="device-flow" aria-label="实时课堂接入链路">
          <div className="device-flow-node">
            <span className="device-flow-icon"><IconCamera size={24} aria-hidden="true" /></span>
            <strong>手机实时采集</strong>
            <span>短时配对与发布授权</span>
          </div>
          <IconChevronRight className="device-flow-arrow" size={22} aria-hidden="true" />
          <div className="device-flow-node">
            <span className="device-flow-icon"><IconVideo size={24} aria-hidden="true" /></span>
            <strong>课堂录像</strong>
            <span>停止后归入当前课堂</span>
          </div>
          <IconChevronRight className="device-flow-arrow" size={22} aria-hidden="true" />
          <div className="device-flow-node">
            <span className="device-flow-icon"><IconReport size={24} aria-hidden="true" /></span>
            <strong>分析与复盘</strong>
            <span>复用证据与人工发布流程</span>
          </div>
        </div>
      </Card>

      <div className="device-step-grid">
        {CONNECTION_STEPS.map((step) => (
          <Card key={step.number} title={`${step.number} · ${step.title}`}>
            <p className="muted">{step.text}</p>
          </Card>
        ))}
      </div>

      <Card title="固定设备接入" subtitle="固定摄像头与 RTSP 管理暂未开放。" actions={<span className="badge badge-gray">规划中</span>}>
        <div className="device-step-grid">
          {FIXED_DEVICE_PLAN.map((step) => (
            <section className="device-plan-item" key={step.number}>
              <h3>{step.number} · {step.title}</h3>
              <p>{step.text}</p>
            </section>
          ))}
        </div>
      </Card>
    </div>
  );
}
