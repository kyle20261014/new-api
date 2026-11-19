import { performance } from 'node:perf_hooks';

const COMPLETED = new Set(['stop', 'tool-calls', 'max-tokens']);

// A small, deterministic local benchmark. It reuses the field naming common to
// public probe reports, but ships its own questions and is not a population IQ
// test or a cross-model ranking.
export const INTELLIGENCE_PROBES = [
  { id: 'candy-canary', dim: 'logic', weight: 1, maxTokens: 256, prompt: '小明有 3 颗糖，吃掉 1 颗后又放入 2 颗。现在有几颗糖？只输出数字。', expected: '4' },
  { id: 'logic-chain', dim: 'logic', weight: 2, maxTokens: 384, prompt: '所有甲都是乙，所有乙都是丙。已知甲存在，能否确定甲是丙？只输出“是”或“否”。', expected: '是' },
  { id: 'logic-order', dim: 'logic', weight: 2, maxTokens: 384, prompt: '甲、乙、丙、丁排队：甲在乙前面，乙在丙前面，丁在丙后面。谁排第二？只输出名字。', expected: '乙' },
  { id: 'logic-converse', dim: 'logic', weight: 2, maxTokens: 384, prompt: '只有今天下雨，小明才带伞。已知小明今天带伞，能否必然推出今天下雨？只输出“是”或“否”。', expected: '是' },
  { id: 'math-arithmetic', dim: 'math', weight: 1, maxTokens: 256, prompt: '计算 48 ÷ 6 + 7 × 3。只输出数字。', expected: '29' },
  { id: 'math-rectangle', dim: 'math', weight: 2, maxTokens: 384, prompt: '长方形周长是 30，长是 9。它的面积是多少？只输出数字。', expected: '54' },
  { id: 'math-sequence', dim: 'math', weight: 2, maxTokens: 384, prompt: '数列 2，6，12，20，30 的下一项是什么？只输出数字。', expected: '42' },
  { id: 'math-hard-roots', dim: 'math-hard', weight: 3, maxTokens: 512, prompt: '整数 x、y 满足 x + y = 17 且 xy = 60。求 x² + y²。只输出数字。', expected: '169' },
  { id: 'math-hard-probability', dim: 'math-hard', weight: 3, maxTokens: 512, prompt: '袋中有 5 个红球和 3 个蓝球，不放回地取 2 个球。两个都是红球的概率是多少？只输出最简分数，例如 1/2。', expected: '5/14' },
  { id: 'math-hard-recurrence', dim: 'math-hard', weight: 3, maxTokens: 512, prompt: '数列满足 a₀ = 0，且 aₙ = 2aₙ₋₁ + 1（n ≥ 1）。求 a₆。只输出数字。', expected: '63' },
  { id: 'code-function', dim: 'code', weight: 2, maxTokens: 768, prompt: '写一个 JavaScript 函数 sumEven(n)，返回 2 到 n（含 n）之间所有偶数的和。只输出代码，不要解释。', expected: 'sumEven' },
  { id: 'code-output', dim: 'code', weight: 2, maxTokens: 384, prompt: '以下 JavaScript 的输出是什么？console.log([1,2,3].map(x => x * 2).filter(x => x > 2).join("-"))。只输出结果。', expected: '4-6' },
  { id: 'instruction-token', dim: 'instruction', weight: 1, maxTokens: 128, prompt: '只输出 DSH-IQ-731，不能有任何其他字符、标点或 Markdown。', expected: 'DSH-IQ-731' },
  { id: 'instruction-json', dim: 'instruction', weight: 2, maxTokens: 256, prompt: '只输出这个 JSON 对象，不要 Markdown 或解释：{"ok":true,"count":3,"tag":"alpha"}', expected: '{"ok":true,"count":3,"tag":"alpha"}' },
  { id: 'instruction-lines', dim: 'instruction', weight: 2, maxTokens: 256, prompt: '只输出三行，顺序必须是 RED、GREEN、BLUE，每行一个词，不要其他文字。', expected: 'RED\nGREEN\nBLUE' },
  { id: 'knowledge-water', dim: 'knowledge', weight: 1, maxTokens: 128, prompt: '水的化学式是什么？只输出化学式。', expected: 'H2O' },
  { id: 'trap-rooster', dim: 'trap', weight: 2, maxTokens: 384, prompt: '一只公鸡在屋顶上下了一个蛋，蛋会往哪边滚？只输出一句简短回答。', expected: '公鸡不会下蛋' },
  { id: 'trap-weight', dim: 'trap', weight: 2, maxTokens: 384, prompt: '1 千克铁和 1 千克棉花哪个更重？只输出“一样重”或“不一样重”。', expected: '一样重' },
];

function textOf(error) {
  return error instanceof Error ? error.message : String(error);
}

function redact(text) {
  return String(text)
    .replace(/Bearer\s+[A-Za-z0-9._~+/=-]+/gi, 'Bearer ***')
    .replace(/\bsk-[A-Za-z0-9_-]{6,}\b/g, 'sk-***')
    .slice(0, 1200);
}

function plainAnswer(text) {
  return String(text || '')
    .trim()
    .replace(/^```(?:json|javascript|js|text|math)?\s*/i, '')
    .replace(/\s*```$/, '')
    .trim();
}

function finalAnswer(text) {
  const answer = plainAnswer(text);
  const marked = answer.match(/(?:^|\n)\s*(?:ANSWER|答案)\s*[:：]\s*([^\n]+)\s*$/i);
  return (marked ? marked[1] : answer).trim().replace(/[。．]+$/, '').trim();
}

function normalizedDigits(text) {
  return String(text).replace(/[０-９]/g, (char) => String.fromCharCode(char.charCodeAt(0) - 65248)).replace(/[／]/g, '/').replace(/\s+/g, '');
}

function exactNumber(reply, expected) {
  const answer = normalizedDigits(finalAnswer(reply)).replace(/^\((.*)\)$/, '$1');
  if (answer === expected) return true;
  if (/^-?\d+(?:\.\d+)?$/.test(answer) && /^-?\d+$/.test(expected)) return Number(answer) === Number(expected);
  return false;
}

export function checkIntelligenceAnswer(id, reply) {
  const answer = finalAnswer(reply);
  const compact = normalizedDigits(answer);
  const probe = INTELLIGENCE_PROBES.find((item) => item.id === id);
  if (!probe) return false;
  if (['candy-canary', 'math-arithmetic', 'math-rectangle', 'math-sequence', 'math-hard-roots', 'math-hard-recurrence'].includes(id)) return exactNumber(reply, probe.expected);
  if (id === 'math-hard-probability') return /^(?:5\/14|\\frac\{5\}\{14\})$/.test(compact);
  if (id === 'logic-chain' || id === 'logic-converse') return /^(?:是|yes)$/i.test(answer);
  if (id === 'logic-order') return /^(?:乙|乙。)$/.test(answer);
  if (id === 'code-output') return compact === '4-6';
  if (id === 'instruction-token') return answer === 'DSH-IQ-731';
  if (id === 'instruction-json') {
    try {
      const value = JSON.parse(answer);
      return value && value.ok === true && value.count === 3 && value.tag === 'alpha' && Object.keys(value).length === 3;
    } catch { return false; }
  }
  if (id === 'instruction-lines') return answer.replace(/\r/g, '').split('\n').map((line) => line.trim()).join('\n') === 'RED\nGREEN\nBLUE';
  if (id === 'knowledge-water') return /^(?:H2O|H₂O)$/i.test(compact);
  if (id === 'trap-rooster') return /公鸡.*(?:不会|不能|不下蛋)|(?:公鸡|雄鸡).*(?:没有|无).*蛋/.test(answer);
  if (id === 'trap-weight') return /一样重|同样重|相等|一样/.test(answer);
  if (id === 'code-function') {
    const body = answer.replace(/\s+/g, ' ');
    const named = /(?:function\s+sumEven\s*\(|(?:const|let|var)\s+sumEven\s*=)/.test(body);
    const loops = /(?:for\s*\(|while\s*\(|\.reduce\s*\(|Array\.from)/.test(body);
    const evenStep = /(?:%\s*2|&\s*1|\+=\s*2|=\s*[A-Za-z_$][\w$]*\s*\+\s*2)/.test(body);
    const formula = /Math\.floor\s*\(\s*n\s*\/\s*2\s*\)/.test(body) && /\*/.test(body);
    const returns = /(?:return|=>)/.test(body);
    return named && returns && (formula || (loops && evenStep));
  }
  return false;
}

async function collectSample(llm, route, probe, signal, timeoutMs) {
  const started = performance.now();
  const controller = new AbortController();
  const abort = () => controller.abort(signal.reason || new Error('测试已停止'));
  if (signal.aborted) abort(); else signal.addEventListener('abort', abort, { once: true });
  const timer = setTimeout(() => controller.abort(new Error('单次智力题超时')), timeoutMs);
  let reply = '';
  let reasoning = '';
  let usage;
  let firstTokenMs;
  let finishKind;
  let failure;
  try {
    const stream = llm.stream({
      ...route,
      messages: [{ role: 'user', content: [{ type: 'text', text: probe.prompt }] }],
      maxTokens: probe.maxTokens,
      signal: controller.signal,
    });
    for await (const chunk of stream) {
      if (firstTokenMs === undefined && ((chunk.type === 'text-delta' || chunk.type === 'reasoning-delta') && chunk.text)) firstTokenMs = Math.round(performance.now() - started);
      if (chunk.type === 'text-delta') reply += chunk.text;
      if (chunk.type === 'reasoning-delta') reasoning += chunk.text;
      if (chunk.type === 'usage') usage = chunk.usage;
      if (chunk.type === 'finish') {
        finishKind = chunk.reason?.kind;
        if (chunk.reason?.kind === 'error' || chunk.reason?.kind === 'aborted') failure = chunk.reason.failure;
      }
    }
    if (failure) throw Object.assign(new Error(failure.message || '模型请求失败'), { code: failure.code, status: failure.status });
    if (!COMPLETED.has(finishKind)) throw new Error(`流未正常结束（${finishKind || '无 finish'}）`);
    const passed = checkIntelligenceAnswer(probe.id, reply);
    return {
      id: probe.id, dim: probe.dim, ok: true, passed, weight: probe.weight, skipped: false,
      note: passed ? '通过' : reply.trim() ? `答案不符，期望 ${probe.expected}` : reasoning.trim() ? '思考内容占用全部输出额度，未给出正式答案' : '无文本答案',
      expected: probe.expected, prompt: probe.prompt, reply: reply.slice(0, 1600), reasoning: reasoning.slice(0, 500),
      reasoningChars: reasoning.length,
      elapsedMs: Math.round(performance.now() - started), firstTokenMs, usage,
    };
  } finally {
    clearTimeout(timer);
    signal.removeEventListener('abort', abort);
  }
}

function reasoningTokens(usage) {
  return Number(usage?.reasoningTokens ?? usage?.reasoning_tokens ?? usage?.outputTokensDetails?.reasoningTokens ?? usage?.outputTokensDetails?.reasoning_tokens) || 0;
}

function makeDetail(probes, items, elapsedMs, startedAt, provider, model, aborted = false) {
  const completedItems = items.filter((item) => item.ok && !item.skipped);
  const correct = items.filter((item) => item.passed === true).length;
  const weightedGot = items.filter((item) => item.passed === true).reduce((sum, item) => sum + item.weight, 0);
  const weightedMax = completedItems.reduce((sum, item) => sum + item.weight, 0);
  const score = weightedMax ? Math.round(100 * weightedGot / weightedMax) : 0;
  const canary = items.find((item) => item.id === 'candy-canary')?.passed === true;
  const skipped = probes.length - completedItems.length;
  const completed = completedItems.length;
  const tier = completed < Math.ceil(probes.length / 2) ? 'insufficient' : !canary || score < 30 ? 'abnormal' : score >= 85 ? 'flagship' : score >= 60 ? 'mid' : 'small';
  const passed = completed === probes.length && canary && score >= 60;
  const totalTokens = items.reduce((sum, item) => sum + reasoningTokens(item.usage), 0);
  const totalReasoningChars = items.reduce((sum, item) => sum + (Number(item.reasoningChars) || 0), 0);
  const latencies = completedItems.map((item) => item.elapsedMs).sort((a, b) => a - b);
  const latencyMs = {
    mean: latencies.length ? Math.round(latencies.reduce((sum, value) => sum + value, 0) / latencies.length) : null,
    median: latencies.length ? latencies[Math.floor(latencies.length / 2)] : null,
    p95: latencies.length ? latencies[Math.max(0, Math.ceil(latencies.length * 0.95) - 1)] : null,
    min: latencies.length ? latencies[0] : null,
    max: latencies.length ? latencies[latencies.length - 1] : null,
  };
  return {
    tier, score, correct, total: probes.length, weighted_got: weightedGot, weighted_max: weightedMax,
    canary, peers: 0, percentile: null, peer_median: null, skipped, items,
    sampling: { mode: 'provider-default', perProbeMaxTokens: Object.fromEntries(probes.map((probe) => [probe.id, probe.maxTokens])) },
    reasoning_tokens: totalTokens, reasoning_chars: totalReasoningChars, completed, partial: completed < probes.length, passed,
    protocol: 'dsh.llm.stream', latency_ms: elapsedMs, latencyMs,
    summary: `智力值 ${score}/100 · ${correct}/${probes.length} 题 · 加权 ${weightedGot}/${weightedMax}`,
    note: '本地18题加权基准；参考公开报告结构，不等同智商或同模型排名。',
    startedAt, provider, model, aborted,
  };
}

export async function intelligenceModel(llm, input, signal = new AbortController().signal) {
  const provider = typeof input.provider === 'string' ? input.provider.trim() : '';
  const model = typeof input.model === 'string' ? input.model.trim() : '';
  if (!provider || !model) throw new Error('provider 和 model 不能为空');
  const route = { provider, model };
  const startedAt = Date.now();
  const started = performance.now();
  const totalTimeoutMs = Math.max(60000, Math.min(900000, Number(input.timeoutMs) || 600000));
  const perProbeTimeoutMs = Math.max(5000, Math.min(90000, Number(input.perProbeTimeoutMs) || 45000));
  const controller = new AbortController();
  const abort = () => controller.abort(signal.reason || new Error('测试已停止'));
  if (signal.aborted) abort(); else signal.addEventListener('abort', abort, { once: true });
  const timer = setTimeout(() => controller.abort(new Error('智力检测总超时')), totalTimeoutMs);
  const items = [];
  try {
    for (const probe of INTELLIGENCE_PROBES) {
      if (controller.signal.aborted) break;
      try {
        items.push(await collectSample(llm, route, probe, controller.signal, perProbeTimeoutMs));
      } catch (error) {
        items.push({ id: probe.id, dim: probe.dim, weight: probe.weight, ok: false, passed: false, skipped: true, code: error?.code || (controller.signal.aborted ? 'ABORTED' : 'PROBE_ERROR'), status: error?.status, note: redact(textOf(error)), message: redact(textOf(error)), prompt: probe.prompt, expected: probe.expected });
        if (controller.signal.aborted) break;
      }
    }
    if (controller.signal.aborted) {
      for (const probe of INTELLIGENCE_PROBES) {
        if (!items.some((item) => item.id === probe.id)) items.push({ id: probe.id, dim: probe.dim, weight: probe.weight, ok: false, passed: false, skipped: true, code: 'ABORTED', note: '未执行', prompt: probe.prompt, expected: probe.expected });
      }
    }
    const elapsedMs = Math.round(performance.now() - started);
    const detail = makeDetail(INTELLIGENCE_PROBES, items, elapsedMs, startedAt, provider, model, controller.signal.aborted);
    const completed = detail.completed;
    const result = {
      ok: completed > 0 && !controller.signal.aborted,
      type: 'intelligence', provider, model, startedAt, elapsedMs, latency_ms: elapsedMs,
      score: detail.score, summary: detail.summary, detail, ...detail,
      partial: detail.partial, code: completed ? (controller.signal.aborted ? 'ABORTED' : detail.partial ? 'PARTIAL' : undefined) : 'INTELLIGENCE_FAILED',
      message: completed ? (controller.signal.aborted ? redact(textOf(controller.signal.reason)) : detail.partial ? '部分题目未完成，分数仅基于已完成的题目' : undefined) : '所有智力题均未完成',
      request: { provider, model, probeCount: INTELLIGENCE_PROBES.length, probeIds: INTELLIGENCE_PROBES.map((probe) => probe.id), stream: true, timeoutMs: totalTimeoutMs, perProbeTimeoutMs },
    };
    return result;
  } finally {
    clearTimeout(timer);
    signal.removeEventListener('abort', abort);
  }
}
