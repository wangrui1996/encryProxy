package proxy

const webUI = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>ENProxy 流量监控</title>

<style>
:root {
    color-scheme: dark;
    --bg: #0b1120;
    --panel: #111c30;
    --panel2: #17243a;
    --border: #263650;
    --text: #e5edf8;
    --muted: #8da0bb;
    --blue: #38bdf8;
    --green: #34d399;
    --orange: #fbbf24;
}

* {
    box-sizing: border-box;
}

body {
    margin: 0;
    padding: 24px;
    background: var(--bg);
    color: var(--text);
    font-family: Arial, "Microsoft YaHei", sans-serif;
}

.container {
    max-width: 1500px;
    margin: auto;
}

.header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    flex-wrap: wrap;
    gap: 15px;
    margin-bottom: 24px;
}

.header h1 {
    margin: 0;
    font-size: 25px;
}

.status {
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--green);
    font-size: 13px;
}

.dot {
    width: 9px;
    height: 9px;
    background: var(--green);
    border-radius: 50%;
    box-shadow: 0 0 10px var(--green);
}

.subtitle {
    color: var(--muted);
    font-size: 13px;
    margin-top: 8px;
}

.grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 16px;
    margin-bottom: 20px;
}

.card {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 20px;
    min-width: 0;
}

.card-title {
    color: var(--muted);
    font-size: 13px;
    margin-bottom: 14px;
}

.card-value {
    font-size: 25px;
    font-weight: 700;
    word-break: break-word;
}

.blue {
    color: var(--blue);
}

.green {
    color: var(--green);
}

.orange {
    color: var(--orange);
}

.unit {
    color: var(--muted);
    font-size: 12px;
    margin-top: 8px;
}

.section {
    margin-top: 22px;
}

.section-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    flex-wrap: wrap;
    gap: 12px;
    margin-bottom: 14px;
}

.section-header h2 {
    margin: 0;
    font-size: 17px;
}

.panel {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 20px;
    overflow: hidden;
}

.table-wrap {
    overflow-x: auto;
}

table {
    width: 100%;
    border-collapse: collapse;
    min-width: 650px;
}

th, td {
    text-align: left;
    padding: 14px 12px;
    border-bottom: 1px solid var(--border);
    font-size: 13px;
    white-space: nowrap;
}

th {
    color: var(--muted);
    font-weight: 500;
}

td {
    color: var(--text);
}

tr:last-child td {
    border-bottom: none;
}

.controls {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
}

select, button {
    background: var(--panel2);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 7px;
    padding: 8px 12px;
    font-size: 12px;
    cursor: pointer;
}

.chart-panel {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 20px;
    margin-bottom: 18px;
}

.chart-container {
    width: 100%;
    height: 290px;
    position: relative;
}

canvas {
    width: 100%;
    height: 100%;
    display: block;
}

.legend {
    display: flex;
    flex-wrap: wrap;
    gap: 14px;
    margin-top: 15px;
}

.legend-item {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--muted);
    font-size: 11px;
}

.legend-color {
    width: 9px;
    height: 9px;
    border-radius: 2px;
}

.footer {
    color: var(--muted);
    font-size: 11px;
    text-align: center;
    padding: 25px 0;
}

@media(max-width: 1000px) {
    .grid {
        grid-template-columns: repeat(2, minmax(0, 1fr));
    }
}

@media(max-width: 550px) {
    body {
        padding: 12px;
    }

    .grid {
        grid-template-columns: 1fr 1fr;
        gap: 9px;
    }

    .card {
        padding: 13px;
    }

    .card-value {
        font-size: 19px;
    }

    .chart-container {
        height: 230px;
    }

    .panel, .chart-panel {
        padding: 13px;
    }
}
</style>
</head>

<body>

<div class="container">

    <div class="header">
        <div>
            <h1>ENProxy 流量监控</h1>
            <div class="subtitle">
                启动时间：<span id="startTime">--</span>
                &nbsp;|&nbsp;
                运行时间：<span id="uptime">--</span>
            </div>
        </div>

        <div class="status">
            <span class="dot"></span>
            <span>实时监控中</span>
            <span id="lastUpdate"></span>
        </div>
    </div>

    <div class="grid">

        <div class="card">
            <div class="card-title">累计上传流量</div>
            <div class="card-value blue" id="totalUpload">--</div>
            <div class="unit">自代理启动以来</div>
        </div>

        <div class="card">
            <div class="card-title">累计下载流量</div>
            <div class="card-value green" id="totalDownload">--</div>
            <div class="unit">自代理启动以来</div>
        </div>

        <div class="card">
            <div class="card-title">实时上传速度</div>
            <div class="card-value blue" id="uploadSpeed">--</div>
            <div class="unit">最近1秒平均速度</div>
        </div>

        <div class="card">
            <div class="card-title">实时下载速度</div>
            <div class="card-value green" id="downloadSpeed">--</div>
            <div class="unit">最近1秒平均速度</div>
        </div>

    </div>

    <div class="section">

        <div class="section-header">
            <h2>监听端口流量统计</h2>
        </div>

        <div class="panel">
            <div class="table-wrap">
                <table>
                    <thead>
                        <tr>
                            <th>监听地址</th>
                            <th>累计上传</th>
                            <th>累计下载</th>
                            <th>实时上传</th>
                            <th>实时下载</th>
                        </tr>
                    </thead>
                    <tbody id="portTable">
                        <tr>
                            <td colspan="5">等待数据...</td>
                        </tr>
                    </tbody>
                </table>
            </div>
        </div>

    </div>

    <div class="section">

        <div class="section-header">
            <h2>最近24小时流量统计</h2>

            <div class="controls">
                <select id="hourMode">
                    <option value="total">总流量</option>
                    <option value="ports">各监听端口</option>
                </select>

                <select id="hourDirection">
                    <option value="both">上传 + 下载</option>
                    <option value="upload">上传</option>
                    <option value="download">下载</option>
                </select>
            </div>
        </div>

        <div class="chart-panel">
            <div class="chart-container">
                <canvas id="hourChart"></canvas>
            </div>
            <div class="legend" id="hourLegend"></div>
        </div>

    </div>

    <div class="section">

        <div class="section-header">
            <h2>最近30天流量统计</h2>

            <div class="controls">
                <select id="dayMode">
                    <option value="total">总流量</option>
                    <option value="ports">各监听端口</option>
                </select>

                <select id="dayDirection">
                    <option value="both">上传 + 下载</option>
                    <option value="upload">上传</option>
                    <option value="download">下载</option>
                </select>
            </div>
        </div>

        <div class="chart-panel">
            <div class="chart-container">
                <canvas id="dayChart"></canvas>
            </div>
            <div class="legend" id="dayLegend"></div>
        </div>

    </div>

    <div class="footer">
        ENProxy Traffic Monitor
    </div>

</div>

<script>

let summaryData = null;
let realtimeData = null;
let hourlyData = null;
let dailyData = null;

const colors = [
    '#38bdf8',
    '#34d399',
    '#fbbf24',
    '#a78bfa',
    '#fb7185',
    '#2dd4bf',
    '#f97316',
    '#818cf8',
    '#e879f9',
    '#a3e635'
];

// =============================
// 格式化
// =============================

function formatBytes(bytes) {

    if (!Number.isFinite(bytes)) {
        return '0 B';
    }

    bytes = Math.max(0, bytes);

    const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];

    let index = 0;

    while (bytes >= 1024 && index < units.length - 1) {
        bytes /= 1024;
        index++;
    }

    return bytes.toFixed(2) + ' ' + units[index];
}

function formatSpeed(bytes) {
    return formatBytes(bytes) + '/s';
}

function formatDuration(seconds) {

    seconds = Math.max(0, seconds);

    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);
    const secs = Math.floor(seconds % 60);

    return days + '天 ' +
        hours + '小时 ' +
        minutes + '分 ' +
        secs + '秒';
}

function escapeHTML(value) {

    return String(value).replace(/[&<>"']/g, function(c) {
        return {
            '&': '&amp;',
            '<': '&lt;',
            '>': '&gt;',
            '"': '&quot;',
            "'": '&#39;'
        }[c];
    });
}

// =============================
// HTTP 请求
// =============================

async function request(url) {

    const response = await fetch(url, {
        cache: 'no-store'
    });

    if (!response.ok) {
        throw new Error('HTTP ' + response.status);
    }

    return await response.json();
}

// =============================
// 累计流量
// =============================

function renderSummary() {

    if (!summaryData) {
        return;
    }

    const total = summaryData.total;

    document.getElementById('totalUpload').textContent =
        formatBytes(total.upload);

    document.getElementById('totalDownload').textContent =
        formatBytes(total.download);

    const start = new Date(summaryData.start_time);

    document.getElementById('startTime').textContent =
        start.toLocaleString();

    const seconds = (Date.now() - start.getTime()) / 1000;

    document.getElementById('uptime').textContent =
        formatDuration(seconds);
}

// =============================
// 实时流量
// =============================

function renderRealtime() {

    if (!realtimeData || !summaryData) {
        return;
    }

    const total = realtimeData.total;

    document.getElementById('uploadSpeed').textContent =
        formatSpeed(total.upload);

    document.getElementById('downloadSpeed').textContent =
        formatSpeed(total.download);

    document.getElementById('lastUpdate').textContent =
        new Date().toLocaleTimeString();

    renderPortTable();
}

// =============================
// 端口统计
// =============================

function renderPortTable() {

    const tbody = document.getElementById('portTable');

    const summaryPorts = summaryData.ports || {};
    const realtimePorts = realtimeData.ports || {};

    const names = Object.keys(summaryPorts).sort();

    if (names.length === 0) {
        tbody.innerHTML =
            '<tr><td colspan="5">暂无监听端口</td></tr>';
        return;
    }

    tbody.innerHTML = names.map(function(name) {

        const total = summaryPorts[name] || {};
        const current = realtimePorts[name] || {};

        return '<tr>' +
            '<td>' + escapeHTML(name) + '</td>' +
            '<td class="blue">' + formatBytes(total.upload || 0) + '</td>' +
            '<td class="green">' + formatBytes(total.download || 0) + '</td>' +
            '<td class="blue">' + formatSpeed(current.upload || 0) + '</td>' +
            '<td class="green">' + formatSpeed(current.download || 0) + '</td>' +
            '</tr>';

    }).join('');
}

// =============================
// 图表绘制
// =============================

function drawChart(canvasId, legendId, data, mode, direction) {

    if (!data || !data.buckets) {
        return;
    }

    const canvas = document.getElementById(canvasId);
    const ctx = canvas.getContext('2d');

    const rect = canvas.getBoundingClientRect();

    const width = rect.width;
    const height = rect.height;

    if (width <= 0 || height <= 0) {
        return;
    }

    const dpr = window.devicePixelRatio || 1;

    canvas.width = width * dpr;
    canvas.height = height * dpr;

    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

    ctx.clearRect(0, 0, width, height);

    const left = 75;
    const right = 15;
    const top = 20;
    const bottom = 35;

    const chartWidth = width - left - right;
    const chartHeight = height - top - bottom;

    const buckets = data.buckets;

    const series = [];

    if (mode === 'total') {

        if (direction === 'both' || direction === 'upload') {

            series.push({
                name: '总上传',
                color: '#38bdf8',
                values: buckets.map(function(b) {
                    return b.total.upload;
                })
            });

        }

        if (direction === 'both' || direction === 'download') {

            series.push({
                name: '总下载',
                color: '#34d399',
                values: buckets.map(function(b) {
                    return b.total.download;
                })
            });

        }

    } else {

        const names = new Set();

        buckets.forEach(function(b) {
            Object.keys(b.ports || {}).forEach(function(name) {
                names.add(name);
            });
        });

        const ports = Array.from(names).sort();

        ports.forEach(function(port, index) {

            const color = colors[index % colors.length];

            if (direction === 'both' || direction === 'upload') {

                series.push({
                    name: port + ' 上传',
                    color: color,
                    values: buckets.map(function(b) {
                        return (b.ports[port] || {}).upload || 0;
                    })
                });

            }

            if (direction === 'both' || direction === 'download') {

                series.push({
                    name: port + ' 下载',
                    color: color,
                    dashed: true,
                    values: buckets.map(function(b) {
                        return (b.ports[port] || {}).download || 0;
                    })
                });

            }

        });

    }

    if (series.length === 0) {
        return;
    }

    let maxValue = 0;

    series.forEach(function(s) {
        s.values.forEach(function(v) {
            maxValue = Math.max(maxValue, v);
        });
    });

    if (maxValue === 0) {
        maxValue = 1;
    }

    // 网格
    ctx.font = '11px Arial';
    ctx.textAlign = 'right';
    ctx.textBaseline = 'middle';

    for (let i = 0; i <= 4; i++) {

        const y = top + chartHeight * i / 4;
        const value = maxValue * (4 - i) / 4;

        ctx.strokeStyle = '#263650';
        ctx.lineWidth = 1;

        ctx.beginPath();
        ctx.moveTo(left, y);
        ctx.lineTo(width - right, y);
        ctx.stroke();

        ctx.fillStyle = '#8da0bb';
        ctx.fillText(formatBytes(value), left - 8, y);

    }

    // 横坐标
    const step = Math.max(1, Math.ceil(buckets.length / 8));

    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';

    buckets.forEach(function(bucket, index) {

        if (index % step !== 0 &&
            index !== buckets.length - 1) {
            return;
        }

        const x = left +
            (buckets.length <= 1 ? 0 :
            index / (buckets.length - 1) * chartWidth);

        const date = new Date(bucket.timestamp * 1000);

        let label;

        if (data.interval === 'hour') {
            label = date.getHours().toString().padStart(2, '0') + ':00';
        } else {
            label = (date.getMonth() + 1) + '/' + date.getDate();
        }

        ctx.fillStyle = '#8da0bb';
        ctx.fillText(label, x, height - bottom + 10);

    });

    // 曲线
    series.forEach(function(s) {

        ctx.beginPath();

        ctx.strokeStyle = s.color;
        ctx.lineWidth = 2;

        ctx.setLineDash(s.dashed ? [5, 4] : []);

        s.values.forEach(function(value, index) {

            const x = left +
                (s.values.length <= 1 ? 0 :
                index / (s.values.length - 1) * chartWidth);

            const y = top +
                chartHeight * (1 - value / maxValue);

            if (index === 0) {
                ctx.moveTo(x, y);
            } else {
                ctx.lineTo(x, y);
            }

        });

        ctx.stroke();
        ctx.setLineDash([]);

    });

    // 图例
    const legend = document.getElementById(legendId);

    legend.innerHTML = series.map(function(s) {

        const style = s.dashed
            ? 'background:transparent;border:2px dashed ' + s.color
            : 'background:' + s.color;

        return '<div class="legend-item">' +
            '<span class="legend-color" style="' + style + '"></span>' +
            '<span>' + escapeHTML(s.name) + '</span>' +
            '</div>';

    }).join('');

}

// =============================
// 图表刷新
// =============================

function renderCharts() {

    drawChart(
        'hourChart',
        'hourLegend',
        hourlyData,
        document.getElementById('hourMode').value,
        document.getElementById('hourDirection').value
    );

    drawChart(
        'dayChart',
        'dayLegend',
        dailyData,
        document.getElementById('dayMode').value,
        document.getElementById('dayDirection').value
    );

}

// =============================
// 数据获取
// =============================

async function refreshRealtime() {

    try {

        const result = await Promise.all([
            request('/api/traffic/summary'),
            request('/api/traffic/realtime')
        ]);

        summaryData = result[0];
        realtimeData = result[1];

        renderSummary();
        renderRealtime();

    } catch (err) {

        console.error('Realtime update failed:', err);

    }

}

async function refreshHistory() {

    try {

        const result = await Promise.all([
            request('/api/traffic/history/hourly'),
            request('/api/traffic/history/daily')
        ]);

        hourlyData = result[0];
        dailyData = result[1];

        renderCharts();

    } catch (err) {

        console.error('History update failed:', err);

    }

}

// =============================
// 页面初始化
// =============================

document.getElementById('hourMode')
    .addEventListener('change', renderCharts);

document.getElementById('hourDirection')
    .addEventListener('change', renderCharts);

document.getElementById('dayMode')
    .addEventListener('change', renderCharts);

document.getElementById('dayDirection')
    .addEventListener('change', renderCharts);

window.addEventListener('resize', renderCharts);

async function init() {

    await refreshRealtime();
    await refreshHistory();

    // 实时数据每秒刷新。
    setInterval(refreshRealtime, 1000);

    // 历史数据每30秒刷新。
    setInterval(refreshHistory, 30000);

}

init();

</script>

</body>
</html>`
