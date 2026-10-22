"""Interactive local model setup. No provider requests and no secret CLI flags."""
import argparse
from decimal import Decimal, InvalidOperation
import getpass
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import uuid

ROOT = Path(__file__).resolve().parents[1]
PRICE_SOURCE = 'https://help.aliyun.com/zh/model-studio/model-pricing'


def pricing(budget):
    if not re.fullmatch(r'\d{1,9}(?:\.\d{1,6})?', budget):
        raise ValueError('预算须为正数人民币金额，最多六位小数。')
    try:
        if Decimal(budget) <= 0:
            raise ValueError('未授权正数预算，不启用真实模型。')
    except InvalidOperation:
        raise ValueError('预算格式不正确。') from None
    return dict(confirmed=True, price_source=PRICE_SOURCE,
        price_version='bailian-beijing-checked-2026-10-22', monthly_budget_cny=budget,
        api_base='https://dashscope.aliyuncs.com/compatible-mode/v1',
        asr_model='qwen3-asr-flash', asr_revision='qwen3-asr-flash',
        asr_billing_mode='seconds', asr_cny_per_second='0.00022',
        asr_input_cny_per_million_tokens='', asr_output_cny_per_million_tokens='',
        report_model='qwen-plus-2025-12-01', report_revision='qwen-plus-2025-12-01',
        report_input_cny_per_million_tokens='0.8', report_output_cny_per_million_tokens='2')


def source_env(key):
    # Restrict to ordinary API key characters; never interpret environment values
    # as shell code or allow interpolation/newline injection into the source file.
    if not re.fullmatch(r'[A-Za-z0-9_-]{12,512}', key):
        raise ValueError('密钥格式不正确；不要输入引号、空格或整段配置。')
    return ('DASHSCOPE_API_KEY=' + key + '\n'
        'BAILIAN_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1\n'
        'BAILIAN_ASR_MODEL=qwen3-asr-flash\nBAILIAN_TEXT_MODEL=qwen-plus-2025-12-01\n'
        'BAILIAN_ASR_LANGUAGE_HINTS=\n')


def write_private(path, text):
    # Never replace another setup file; preserve the existing deployment config.
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as stream:
        stream.write(text)


def main():
    import team_web as web
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--data-root', type=Path, default=ROOT / 'var/team-web')
    args = parser.parse_args()
    if not sys.stdin.isatty():
        raise ValueError('请在本机交互窗口运行，不能通过管道传入密钥。')
    state = args.data_root.resolve(strict=True)
    config = web.load(state / 'private.json')
    if web.sql(config, state, "SELECT count(*) FROM teaching.analysis_jobs WHERE status IN('queued','running')") != '0':
        raise ValueError('请先等待当前分析结束或在网页取消任务，再配置模型。')
    print('选择：阿里云百炼华北2（北京），语音 qwen3-asr-flash，报告 qwen-plus-2025-12-01。')
    print('官方价格快照（2026-10-22）：语音0.00022元/秒；报告输入0.8、输出2元/百万token（本配置限制在首档）。')
    print('语音名称为供应商滚动别名，效果可能随供应商更新；不是固定权重版本。')
    print('密钥需在你自己的百炼账号中创建，不能使用聊天产品会员代替。价格变化时先停止并更新配置。')
    print('本工具不测试收费接口；填写后会保存配置并重启本机服务，网页提交获准分析时才可能产生费用。')
    print('应用预算按本月统计，不是供应商账户总消费上限；未知账单继续阻止自动重复调用。')
    key = getpass.getpass('粘贴北京地域API Key（隐藏输入，按Ctrl+C取消）：').strip()
    source = source_env(key)
    budget = input('你允许的应用本月预算（元，留空取消；不会自动填金额）：').strip()
    if not budget:
        print('已取消，没有保存密钥或启用模型。'); return
    prices = pricing(budget)
    env = web.environment(config, state)
    binary = state / 'bin/model-config.exe'
    subprocess.run([config['go'], 'build', '-o', str(binary), './cmd/model-config'], cwd=ROOT, env=env, check=True)
    tag = uuid.uuid4().hex
    src = state / ('.env.model-setup-' + tag)
    worksheet = state / ('model-pricing-' + tag + '.private.json')
    output = state / ('analysis-' + tag + '.private.json')
    write_private(worksheet, json.dumps(prices, ensure_ascii=False, indent=2) + '\n')
    source_written = False
    try:
        write_private(src, source)
        source_written = True
        subprocess.run([str(binary), 'prepare', '-demo-env', str(src), '-pricing', str(worksheet), '-output', str(output)], cwd=ROOT, check=True)
    finally:
        if source_written and src.exists(): src.unlink()  # Only this attempt's file.
    print('配置已通过离线校验。尚未检验账号额度、真实识别效果或供应商连通性。')
    web.stop(config, state)
    command = [config['python'], str(ROOT/'tools/team_web.py'), '--data-root', str(state), 'prepare',
        '--go', config['go'], '--pg-bin', config['pg_bin'], '--node', config['node'],
        '--python', config['python'], '--models', config['models'], '--analysis-config', str(output)]
    subprocess.run(command, cwd=ROOT, check=True)
    subprocess.run([config['python'], str(ROOT/'tools/team_web.py'), '--data-root', str(state), 'start-detached'], cwd=ROOT, check=True)
    print('已启用。到课堂详情选“语音转写与教学报告”；完成后在“转写”和“报告”查看。')


if __name__ == '__main__':
    try:
        main()
    except (KeyboardInterrupt, EOFError):
        print('\n已取消。')
    except Exception:
        # Do not echo exception arguments or local credential contents.
        print('配置未完成。请检查账号、预算、当前任务及本机启动日志；已有模型请求不会自动重发。', file=sys.stderr)
        raise SystemExit(1)
