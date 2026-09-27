import { createSignal, For, onCleanup, onMount, Show } from 'solid-js';
import { authFetch } from '../services/httpAuth';
import styles from './OrderSessionsPanel.module.css';

type Order = {
  order_id: string; session_reference: string; device_id: string; state: string; remaining: number;
  expires_at: string; reason?: string;
  request: { product_name: string; bundle_id: string; price: number; quantity: number; currency: string };
};
const states: Record<string, string> = { preparing: '正在准备', active: '使用中', closing: '正在回收', pending_cleanup: '待清理', quarantined: '已隔离', completed: '已完成' };

export default function OrderSessionsPanel() {
  const [orders, setOrders] = createSignal<Order[]>([]);
  const [error, setError] = createSignal('');
  const [loading, setLoading] = createSignal(false);
  const [pending, setPending] = createSignal('');
  let disposed = false;
  const controller = new AbortController();
  async function refresh() {
    if (loading()) return;
    setLoading(true);
    try {
      const response = await authFetch('/api/order-sessions', { signal: controller.signal });
      if (response.status === 404) throw new Error('订单服务尚未启用。配置设备档案后再开启。');
      if (!response.ok) throw new Error('无法读取订单，请确认后台连接和登录状态。');
      const data = await response.json();
      if (!Array.isArray(data)) throw new Error('当前服务未提供订单接口，请检查服务端版本和配置。');
      if (!disposed) { setOrders(data); setError(''); }
    } catch (e) { if (!disposed) setError(e instanceof Error ? e.message : '读取失败'); }
    finally { if (!disposed) setLoading(false); }
  }
  async function release(order: Order) {
    if (!window.confirm(`结束订单 ${order.order_id} 并回收手机？只有清理验证通过后，设备才能再次分配。`)) return;
    setPending(order.order_id);
    try {
      const response = await authFetch(`/api/order-sessions/${encodeURIComponent(order.session_reference)}/release`, { method: 'POST', signal: controller.signal });
      if (!response.ok) throw new Error('回收请求失败，请刷新状态后重试。');
      await refresh();
    } catch (e) { if (!disposed) setError(e instanceof Error ? e.message : '回收失败'); }
    finally { if (!disposed) setPending(''); }
  }
  async function confirmClean(order: Order) {
    if (!window.confirm(`请确认你已清理订单 ${order.order_id} 的游戏登录信息，并验证下一位用户无法访问上一个账号。确认后手机将重新参与分配。`)) return;
    setPending(order.order_id);
    try {
      const response = await authFetch(`/api/order-sessions/${encodeURIComponent(order.session_reference)}/confirm-clean`, { method: 'POST', signal: controller.signal });
      if (!response.ok) throw new Error('确认失败，请刷新并检查设备状态。');
      await refresh();
    } catch (e) { if (!disposed) setError(e instanceof Error ? e.message : '确认失败'); }
    finally { if (!disposed) setPending(''); }
  }
  onMount(() => { refresh(); });
  const timer = setInterval(refresh, 5000);
  onCleanup(() => { disposed = true; clearInterval(timer); controller.abort(); });
  return <section class={styles.panel} aria-label="订单会话" lang="zh-CN">
    <div class={styles.heading}><div><h2>订单会话</h2><p>每次价格完全匹配 · 使用上限 6 分钟 · 到期待清理，确认后归还设备</p></div>
      <button class={styles.button} onClick={refresh} disabled={loading()}>{loading() ? '正在刷新' : '刷新订单'}</button></div>
    <Show when={error()}><p role="alert" class={styles.message}>{error()}</p></Show>
    <Show when={!error() && !loading() && orders().length === 0}><p class={styles.message}>暂无订单。厦门通过接口创建订单后，会显示在这里。</p></Show>
    <div class={styles.tableWrap}><table><caption class={styles.caption}>每 5 秒刷新。次数在购买请求被放行时扣除，不代表游戏已经到账。</caption>
      <thead><tr><th>订单 / 商品</th><th>设备</th><th>价格 / 次数</th><th>状态</th><th>到期时间</th><th>操作</th></tr></thead>
      <tbody><For each={orders()}>{order => <tr>
        <td><strong>{order.order_id}</strong><span>{order.request.product_name || order.request.bundle_id}</span></td>
        <td>{order.device_id}</td><td>{order.request.currency} {order.request.price.toFixed(2)}<span>剩余 {order.remaining} / {order.request.quantity} 次</span></td>
        <td>{states[order.state] || order.state}<Show when={order.reason}><span>{order.reason}</span></Show></td>
        <td>{order.expires_at.startsWith('0001') ? '准备完成后计时' : new Date(order.expires_at).toLocaleString('zh-CN')}</td>
        <td><Show when={order.state !== 'completed'} fallback="已归还"><button class={styles.button} disabled={!!pending() || order.state === 'closing'} onClick={() => order.state === 'pending_cleanup' ? confirmClean(order) : release(order)}>{pending() === order.order_id ? '正在提交' : order.state === 'pending_cleanup' ? '确认已清理' : order.state === 'quarantined' ? '重试回收' : '结束并回收'}</button></Show></td>
      </tr>}</For></tbody>
    </table></div>
  </section>;
}
