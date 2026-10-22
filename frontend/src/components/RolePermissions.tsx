import type { ReactElement } from 'react';
import { ROLE_CODES } from '../data/authApi';
import { roleLabel } from '../format';
import { Card } from './ui';

/** Read-only explanation of 项目文档 §2; authorization remains in Go. */
const rows: readonly (readonly [string, string, string, string, string])[] = [
  ['维护学院组织、学期、教室', '全校', '无', '无', '无'],
  ['维护课程、班级、开课、课表', '全校', '本学院', '无', '无'],
  ['创建一次课堂', '全校', '本学院', '无', '本人开课'],
  ['查看课堂目录', '全校', '本学院', '授权学院', '本人开课'],
  ['上传录像、提交或取消分析', '全校', '无', '无', '本人课堂'],
  ['查看录像、转写、证据、报告', '全校', '无', '授权学院', '本人课堂'],
  ['修改转写、编辑报告草稿', '全校', '无', '授权学院', '本人课堂'],
  ['复核、发布、撤回报告', '全校', '无', '授权学院', '无'],
  ['导出当前发布版报告', '全校', '无', '授权学院', '本人课堂'],
  ['核验来源及允许用途', '全校', '本学院', '无', '仅提交说明'],
  ['管理账号及授予角色', '全校，受管理员保护规则限制', '仅可管理范围内的他人教师、督导', '无', '无'],
  ['修改本人密码', '验证旧密码后可修改', '验证旧密码后可修改', '验证旧密码后可修改', '验证旧密码后可修改'],
  ['归档课堂目录', '全校', '本学院', '无', '无'],
  ['彻底清理课堂', '全校，须明确确认', '无', '无', '无'],
  ['查询操作记录', '全部已保存记录', '仅本人记录', '仅本人记录', '无'],
  ['查看运营账目、核对模型费用', '全校', '无', '无', '无'],
];

export function RolePermissions({ onClose }: { onClose(): void }): ReactElement {
  return <Card title="角色权限对照表" subtitle="查看每种角色能做什么，以及能处理哪个范围的数据。" actions={<button type="button" className="btn btn-secondary btn-sm" onClick={onClose}>收起权限对照表</button>}>
    <div className="table-wrap"><table className="table"><caption className="sr-only">系统四种固定角色的操作与数据范围</caption><thead><tr><th scope="col">操作</th>{ROLE_CODES.map(role => <th scope="col" key={role}>{roleLabel(role)}</th>)}</tr></thead><tbody>{rows.map(([operation, ...permissions]) => <tr key={operation}><th scope="row">{operation}</th>{permissions.map((text, index) => <td key={ROLE_CODES[index]}>{text}</td>)}</tr>)}</tbody></table></div>
    <p className="field-hint">“无”表示该角色单独不具备此操作权限。一个账号兼任多个角色时，各角色的操作及范围分别生效，不能把不同角色的范围拼成新的权限；教师兼任督导可以按督导范围复核，复核本人课堂会记录“本人复核”。</p>
    <p className="field-hint">实际操作还须满足录像来源许可、数据有效期和任务状态等条件。此表用于查看规则；授予或撤销角色请打开对应账号资料，不能在此表勾选修改。</p>
  </Card>;
}
