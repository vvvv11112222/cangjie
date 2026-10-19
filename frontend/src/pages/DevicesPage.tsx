import type { ReactElement } from 'react';
import { Card } from '../components/ui';
import { IconCamera, IconChevronRight, IconInfo, IconReport, IconVideo } from '../components/icons';

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

      <p className="notice-strip">
        <IconInfo size={14} aria-hidden="true" />
        当前已接入按课堂发起的手机实时采集；独立教室摄像头目录、RTSP 设备列表和跨课堂在线状态不在现有协议中，页面不会伪造这些数据。
      </p>

      <Card title="实时课堂接入链路" subtitle="采集只是媒体入口，权限、录像归属与课后分析仍由 Go 服务统一管理。">
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

      <Card title="当前能力边界" subtitle="页面只说明仓库里已经存在的真实能力。">
        <dl className="device-capability-list">
          <div><dt>可以做</dt><dd>在具体课堂中创建采集、生成手机配对入口、实时观看、停止采集，并将录像接入课后分析。</dd></div>
          <div><dt>必须依赖</dt><dd>接口模式、已登录账号、服务端采集配置及媒体服务；固定样例模式不生成虚假直播画面。</dd></div>
          <div><dt>尚未提供</dt><dd>独立设备资产目录、RTSP 摄像头登记、所有课堂采集汇总和设备在线状态查询。</dd></div>
        </dl>
      </Card>
    </div>
  );
}
