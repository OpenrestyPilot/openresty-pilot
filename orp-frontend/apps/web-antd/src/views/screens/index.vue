<script setup lang="ts">
import { Select } from 'ant-design-vue';
import { computed, onMounted, ref } from 'vue';

interface AdminArea {
  code: string;
  name: string;
  level: number;
  children: AdminArea[];
}

interface ScreenCard {
  name: string;
  code: string;
  level: number;
  description: string;
  mode: '2d' | '3d';
  key: string;
  scene?: string;
}

const screenBase = import.meta.env.VITE_ORP_SCREEN_URL || '/screen/';
const adminIndex = ref<AdminArea | null>(null);
const provinceCode = ref('');
const cityCode = ref('');
const districtCode = ref('');
const loadError = ref('');

onMounted(async () => {
  try {
    const response = await fetch('/maps/admin-index.json');
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    adminIndex.value = await response.json();
  } catch (error) {
    loadError.value = `行政区列表加载失败（${String(error)}），仍可打开全国大屏。`;
  }
});

const provinces = computed(() => adminIndex.value?.children ?? []);
const provinceOptions = computed(() => [
  { label: '全国', value: '' },
  ...provinces.value.map((area) => ({ label: `${area.name}（${area.code}）`, value: area.code })),
]);
const selectedProvince = computed(() => provinces.value.find((area) => area.code === provinceCode.value));
const isMunicipality = computed(() => ['110000', '120000', '310000', '500000'].includes(provinceCode.value));
const cities = computed(() => provinces.value.find((area) => area.code === provinceCode.value)?.children ?? []);
const cityOptions = computed(() => [
  { label: selectedProvince.value ? `全${isMunicipality.value ? '市' : '省'}范围` : '请先选择省级行政区', value: '' },
  ...cities.value.map((area) => ({ label: `${area.name}（${area.code}）`, value: area.code })),
]);
const districts = computed(() => cities.value.find((area) => area.code === cityCode.value)?.children ?? []);
const districtOptions = computed(() => [
  { label: cityCode.value ? '全市范围' : '请先选择市级行政区', value: '' },
  ...districts.value.map((area) => ({ label: `${area.name}（${area.code}）`, value: area.code })),
]);
const selectedArea = computed(() => {
  if (districtCode.value) return districts.value.find((area) => area.code === districtCode.value);
  if (cityCode.value) return cities.value.find((area) => area.code === cityCode.value);
  if (provinceCode.value) return provinces.value.find((area) => area.code === provinceCode.value);
  return adminIndex.value;
});
const selectedPath = computed(() => [
  adminIndex.value?.name,
  provinces.value.find((area) => area.code === provinceCode.value)?.name,
  cities.value.find((area) => area.code === cityCode.value)?.name,
  districts.value.find((area) => area.code === districtCode.value)?.name,
].filter(Boolean).join(' / '));
const hasAreaSelection = computed(() => Boolean(provinceCode.value || cityCode.value || districtCode.value));
const areaLevel = computed(() => {
  if (districtCode.value) return '区县级';
  if (cityCode.value) return selectedArea.value?.level === 3 ? '区县级' : '市级';
  if (provinceCode.value) return '省级';
  return '全国';
});
const areaCount = computed(() => {
  if (districtCode.value) return 1;
  if (cityCode.value) return districts.value.length;
  if (provinceCode.value) return cities.value.length;
  return provinces.value.length;
});
const lowerAreaLabel = computed(() => isMunicipality.value ? '区县' : '市级行政区');
const areaCountLabel = computed(() => {
  if (districtCode.value) return '所选区县';
  if (cityCode.value) return '个下级区县';
  if (provinceCode.value) return isMunicipality.value ? '个区县' : '个下级市区';
  return '个省级区域';
});

// China has 23 provinces; municipalities, autonomous regions, and SARs remain
// available through the administrative selectors above.
const provinceCodes = new Set([
  '130000', '140000', '210000', '220000', '230000', '320000', '330000',
  '340000', '350000', '360000', '370000', '410000', '420000', '430000',
  '440000', '460000', '510000', '520000', '530000', '610000', '620000',
  '630000', '710000',
]);
const chinaProvinces = computed(() => provinces.value.filter((area) => provinceCodes.has(area.code)));

const cards = computed<ScreenCard[]>(() => {
  const area = selectedArea.value;
  const entries = hasAreaSelection.value && area
    ? [{ name: area.name, code: area.code, level: area.level, description: `${selectedPath.value || area.name} · ${areaLevel.value}` }]
    : [
        { name: '世界地图', code: 'world', level: 0, description: '全球实时流量态势' },
        ...chinaProvinces.value.map((province) => ({
          name: province.name,
          code: province.code,
          level: province.level,
          description: `${province.name} · 省级地图与下钻浏览`,
        })),
      ];
  const mapCards: ScreenCard[] = entries.flatMap((entry) => (['2d', '3d'] as const).map((mode) => ({
    ...entry,
    mode,
    key: `${entry.code}-${mode}`,
  })));
  if (!hasAreaSelection.value) {
    mapCards.splice(2, 0, {
      name: '点阵地球', code: 'world', level: 0,
      description: '浅青色全球点阵 · 支持拖动旋转与 GeoIP 实时流线',
      mode: '3d' as const, scene: 'globe', key: 'world-globe',
    });
    mapCards.splice(3, 0, {
      name: '全球流量态势', code: 'world', level: 0,
      description: '浅色世界点阵 · 真实 GeoIP 弧线与流动粒子',
      mode: '2d' as const, scene: 'globe-light', key: 'world-globe-light',
    });
  }
  return mapCards;
});

function openScreen(mode: '2d' | '3d', areaCode = selectedArea.value?.code || '100000', scene?: string) {
  const url = new URL(screenBase, window.location.origin);
  url.searchParams.set('map', areaCode);
  url.searchParams.set('mode', mode);
  if (scene) url.searchParams.set('scene', scene);
  try {
    const savedTheme = localStorage.getItem('orp-screen-theme');
    if (savedTheme === 'light' || savedTheme === 'dark') url.searchParams.set('theme', savedTheme);
  } catch { /* 大屏仍可通过页面内的主题按钮切换 */ }
  window.open(url.toString(), '_blank', 'noopener,noreferrer');
}

function onProvinceChange() {
  cityCode.value = '';
  districtCode.value = '';
}

function onCityChange() {
  districtCode.value = '';
}
</script>

<template>
  <main class="screen-gallery">
    <header class="gallery-heading">
      <div>
        <div class="eyebrow">REALTIME REQUEST MONITORING</div>
        <h1>大屏展示</h1>
        <p>选择行政区域，打开对应的实时流量地图。</p>
      </div>
      <div class="heading-actions"><div class="screen-count"><span class="status-dot"></span>23 个省 · 每省 2D / 3D</div></div>
    </header>

    <section class="area-selector" aria-label="选择行政区域">
      <div class="selector-heading">
        <div><h2>选择展示范围</h2><p>支持中国省级、市级与区县级边界，可在大屏中继续下钻。</p></div>
        <div class="area-stat"><strong>{{ areaCount }}</strong><span>{{ areaCountLabel }}</span></div>
      </div>
      <div class="select-row">
        <label><span>省级行政区</span><Select v-model:value="provinceCode" :options="provinceOptions" class="region-select" option-filter-prop="label" placeholder="搜索省级行政区" show-search @change="onProvinceChange" /></label>
        <label><span>{{ lowerAreaLabel }}</span><Select v-model:value="cityCode" :disabled="!provinceCode || !cities.length" :options="cityOptions" class="region-select" option-filter-prop="label" placeholder="搜索市级行政区" show-search @change="onCityChange" /></label>
        <label><span>区县</span><Select v-model:value="districtCode" :disabled="!cityCode" :options="districtOptions" class="region-select" option-filter-prop="label" placeholder="搜索区县名称或代码" show-search /></label>
      </div>
      <div class="selected-area"><span class="pin">⌖</span><span>{{ selectedPath || '中国' }}</span><span class="level-tag">{{ areaLevel }}</span></div>
      <p v-if="loadError" class="load-error" role="alert">{{ loadError }}</p>
    </section>

    <section class="mode-section" aria-label="选择地图模式">
      <div class="section-title"><div><h2>{{ hasAreaSelection ? `${selectedArea?.name}大屏` : '省级地图大屏' }}</h2><p>{{ hasAreaSelection ? '所选区域支持 2D 与 3D 地图，在新标签页打开。' : '展示 23 个省份的 2D、3D 地图，并可通过上方筛选器切换到市、区县。' }}</p></div><span class="region-badge">{{ hasAreaSelection ? areaLevel : `${chinaProvinces.length} 个省份` }}</span></div>
      <div class="mode-grid">
        <article v-for="card in cards" :key="card.key" class="mode-card">
          <div class="map-preview" :class="[card.mode === '2d' ? 'preview-2d' : 'preview-3d', card.code === 'world' ? 'preview-world' : '', card.scene === 'globe' ? 'preview-globe' : '', card.scene === 'globe-light' ? 'preview-globe-light' : '']" aria-hidden="true"><div v-if="card.scene === 'globe'" class="preview-orb"><i v-for="dot in 90" :key="dot" :style="{ '--dot-x': `${(dot * 37) % 100}%`, '--dot-y': `${(dot * 61) % 100}%`, '--dot-opacity': `${0.35 + (dot % 6) * 0.1}` }"></i></div><template v-else><div class="grid-lines"></div><div v-if="card.code === 'world'" class="world-shapes"><i></i><i></i><i></i><i></i><i></i></div><div v-else class="silhouette" :class="card.mode === '2d' ? 'silhouette-flat' : 'silhouette-raised'"></div><i class="glow-dot dot-a"></i><i class="glow-dot dot-b"></i><i class="glow-dot dot-c"></i></template><div class="preview-caption">{{ card.scene === 'globe-light' ? 'GLOBAL GEOIP FLOW' : card.scene === 'globe' ? 'GLOBAL GEOIP GLOBE' : card.code === 'world' ? 'GLOBAL OVERVIEW' : 'GEOGRAPHIC OVERVIEW' }} <b>{{ card.scene === 'globe' ? '3D' : card.scene === 'globe-light' ? 'LIVE' : card.mode.toUpperCase() }}</b></div></div>
          <div class="mode-card-body"><div><span class="mode-kicker">{{ card.scene === 'globe-light' ? 'LIGHT GEOIP MAP' : card.scene === 'globe' ? 'DOTTED GLOBE' : card.mode === '2d' ? 'FLAT MAP' : 'SPATIAL MAP' }}</span><h3>{{ card.name }} · {{ card.scene === 'globe' ? '3D' : card.scene === 'globe-light' ? '动态点阵' : card.mode.toUpperCase() }}</h3><p>{{ card.description }} · {{ card.scene === 'globe' ? '拖动旋转地球' : card.scene === 'globe-light' ? '地图扫描与真实流量粒子' : card.mode === '2d' ? '平面地图' : '立体地图' }}</p></div><button type="button" @click="openScreen(card.mode, card.code, card.scene)">新标签页打开 <span>↗</span></button></div>
        </article>
      </div>
    </section>
    <footer class="gallery-footnote">地图展示中国行政区划边界。实时来源点和流线依赖已配置的 GeoIP 数据。</footer>
  </main>
</template>

<style scoped>
.screen-gallery { min-height: 100%; padding: 28px clamp(20px, 3vw, 44px) 34px; background: radial-gradient(ellipse at 82% 0%, rgb(37 99 235 / 7%), transparent 34%); }
.gallery-heading,.selector-heading,.section-title { display: flex; align-items: flex-end; justify-content: space-between; gap: 24px; }
.gallery-heading { margin: 4px 0 24px; }.eyebrow,.mode-kicker { color: #4f8df7; font-size: 10px; font-weight: 700; letter-spacing: .16em; }
.gallery-heading h1 { margin: 8px 0 5px; color: #17243a; font-size: 28px; font-weight: 650; letter-spacing: -.03em; }.gallery-heading p,.selector-heading p,.section-title p { margin: 0; color: #8290a5; font-size: 13px; }
.screen-count { display: inline-flex; align-items: center; gap: 8px; padding: 8px 12px; border: 1px solid #e8edf5; border-radius: 999px; color: #718097; background: rgb(255 255 255 / 72%); font-size: 12px; white-space: nowrap; }.status-dot { width: 7px; height: 7px; border-radius: 50%; background: #27c68a; box-shadow: 0 0 0 3px rgb(39 198 138 / 12%); }
.heading-actions { display: flex; align-items: center; gap: 8px; }.world-link { border: 1px solid #dce7f6; border-radius: 999px; padding: 7px 10px; color: #55759f; background: #fff; cursor: pointer; font-size: 10px; white-space: nowrap; }.world-link:hover { border-color: #a9c6ee; color: #286bcf; background: #f7faff; }
.area-selector,.mode-section { border: 1px solid #e8edf5; border-radius: 13px; background: #fff; box-shadow: 0 5px 20px rgb(31 48 74 / 4%); }.area-selector { padding: 21px 23px 17px; }.selector-heading h2,.section-title h2 { margin: 0 0 5px; color: #1d2a3d; font-size: 16px; font-weight: 600; }.area-stat { display: flex; align-items: baseline; gap: 6px; color: #8693a5; font-size: 11px; }.area-stat strong { color: #3276df; font-size: 20px; font-weight: 650; }
.select-row { display: grid; grid-template-columns: repeat(3,minmax(0,1fr)); gap: 14px; margin-top: 19px; }.select-row label { display: flex; flex-direction: column; gap: 6px; color: #6c7b91; font-size: 11px; }.region-select { width: 100%; color: #2b3b52; font-size: 12px; }.region-select :deep(.ant-select-selector) { height: 38px !important; border-color: #e1e7f0 !important; border-radius: 7px !important; padding: 3px 10px !important; }.region-select :deep(.ant-select-selection-item),.region-select :deep(.ant-select-selection-placeholder) { line-height: 30px !important; }.region-select:deep(.ant-select-focused .ant-select-selector) { border-color: #79a9f5 !important; box-shadow: 0 0 0 3px rgb(52 120 229 / 10%) !important; }
.selected-area { display: flex; align-items: center; gap: 8px; margin-top: 14px; color: #5a6b82; font-size: 12px; }.pin { color: #397ee5; font-size: 16px; }.level-tag,.region-badge { border-radius: 5px; padding: 4px 8px; color: #55759f; background: #f0f5fc; font-size: 10px; }.load-error { margin: 10px 0 0; color: #c56548; font-size: 11px; }
.mode-section { margin-top: 19px; padding: 20px 23px 22px; }.section-title { align-items: center; margin-bottom: 16px; }.mode-grid { display: grid; grid-template-columns: repeat(3,minmax(0,1fr)); gap: 16px; }.mode-card { overflow: hidden; border: 1px solid #e8edf5; border-radius: 10px; background: #fff; transition: transform .18s ease,box-shadow .18s ease; }.mode-card:hover { transform: translateY(-3px); box-shadow: 0 10px 24px rgb(31 48 74 / 10%); }.map-preview { position: relative; height: clamp(155px,16vw,200px); overflow: hidden; background: radial-gradient(ellipse at 50% 50%,#12386b,#08172f 67%,#061021); }.grid-lines { position: absolute; inset: 0; opacity: .36; background-image: linear-gradient(rgb(83 159 237 / 9%) 1px,transparent 1px),linear-gradient(90deg,rgb(83 159 237 / 9%) 1px,transparent 1px); background-size: 27px 27px; }.silhouette { position: absolute; top: 17%; left: 36%; width: 29%; height: 65%; background: linear-gradient(135deg,rgb(37 170 206 / 65%),rgb(27 86 164 / 56%)); clip-path: polygon(17% 0,55% 5%,70% 16%,90% 15%,95% 29%,83% 38%,100% 53%,78% 62%,72% 78%,56% 88%,49% 100%,35% 87%,29% 70%,15% 65%,18% 48%,0 37%,12% 22%); border: 1px solid #62ddf5; filter: drop-shadow(0 0 12px #28b6e3); }.silhouette-flat { transform: scale(1.1); }.silhouette-raised { transform: perspective(380px) rotateX(48deg) rotateZ(-14deg) scale(1.08); filter: drop-shadow(6px 12px 7px rgb(0 0 0 / 55%)) drop-shadow(0 0 11px #28b6e3); }.world-shapes { position: absolute; inset: 17% 12% 20%; filter: drop-shadow(0 0 12px #28b6e3); }.world-shapes i { position: absolute; display: block; background: linear-gradient(135deg,rgb(37 170 206 / 75%),rgb(27 86 164 / 65%)); border: 1px solid #62ddf5; }.world-shapes i:nth-child(1) { left: 3%; top: 9%; width: 23%; height: 46%; clip-path: polygon(20% 0,85% 7%,100% 27%,75% 43%,86% 65%,59% 100%,35% 78%,24% 55%,0 30%); }.world-shapes i:nth-child(2) { left: 20%; top: 54%; width: 14%; height: 38%; clip-path: polygon(18% 0,88% 12%,100% 43%,63% 100%,35% 76%,0 33%); }.world-shapes i:nth-child(3) { left: 42%; top: 8%; width: 18%; height: 42%; clip-path: polygon(10% 11%,44% 0,80% 20%,100% 45%,73% 64%,84% 100%,31% 81%,0 48%); }.world-shapes i:nth-child(4) { right: 4%; top: 14%; width: 31%; height: 48%; clip-path: polygon(0 18%,28% 0,55% 9%,71% 27%,100% 18%,89% 53%,70% 58%,60% 86%,39% 100%,27% 68%,6% 54%); }.world-shapes i:nth-child(5) { right: 16%; bottom: 4%; width: 9%; height: 25%; clip-path: polygon(32% 0,88% 23%,100% 61%,53% 100%,0 55%); }.preview-world.preview-3d .world-shapes { transform: perspective(380px) rotateX(40deg) rotateZ(-7deg); }.glow-dot { position: absolute; z-index: 1; width: 7px; height: 7px; border-radius: 50%; background: #8ef2ff; box-shadow: 0 0 12px 3px rgb(55 198 255 / 75%); }.dot-a { top: 37%; left: 46%; }.dot-b { top: 55%; left: 55%; }.dot-c { top: 68%; left: 42%; }.preview-caption { position: absolute; right: 13px; bottom: 11px; left: 14px; display: flex; align-items: center; justify-content: space-between; color: #83a9ca; font: 9px Consolas,monospace; letter-spacing: .12em; }.preview-caption b { border: 1px solid rgb(82 177 234 / 32%); border-radius: 4px; padding: 3px 6px; color: #99dfff; font-size: 9px; font-weight: 500; }
.map-preview.preview-globe { background: radial-gradient(ellipse at 50% 70%,rgb(113 188 194 / 24%),transparent 37%),linear-gradient(180deg,#fff,#f7fafb); }.preview-globe .preview-caption { color: #789397; }.preview-globe .preview-caption b { border-color: #d7e8e8; color: #5b9499; }.preview-orb { position: absolute; top: 50%; left: 50%; width: 142px; height: 142px; overflow: hidden; transform: translate(-50%,-50%); border-radius: 50%; background: radial-gradient(circle at 35% 30%,#c6f3ef,#83d9d7 62%,#54bdc5); box-shadow: 0 10px 26px rgb(81 181 187 / 25%),0 0 0 12px rgb(207 229 230 / 21%),0 0 0 24px rgb(231 240 241 / 28%); }.preview-orb i { position: absolute; top: var(--dot-y); left: var(--dot-x); width: 2px; height: 2px; border-radius: 50%; background: white; opacity: var(--dot-opacity); }
.map-preview.preview-globe-light { background: linear-gradient(180deg,#f8faff,#e9eef8); }.preview-globe-light .grid-lines { opacity: .55; background-image: linear-gradient(rgb(115 139 188 / 10%) 1px,transparent 1px),linear-gradient(90deg,rgb(115 139 188 / 10%) 1px,transparent 1px); }.preview-globe-light .world-shapes { inset: 19% 13% 22%; opacity: .95; filter: drop-shadow(0 0 8px rgb(145 166 211 / 28%)); }.preview-globe-light .world-shapes i { border: 1px solid rgb(255 255 255 / 90%); background: radial-gradient(circle,#fff 1px,transparent 1.5px); background-size: 4px 4px; }.preview-globe-light .preview-caption { color: #71809b; }.preview-globe-light .preview-caption b { border-color: #d9e1ef; color: #7182b4; }
.mode-card-body { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 16px 18px; }.mode-kicker { color: #94a6bf; font-size: 9px; }.mode-card h3 { margin: 3px 0 3px; color: #1d2a3d; font-size: 15px; font-weight: 600; }.mode-card p { margin: 0; color: #8491a3; font-size: 11px; }.mode-card button { flex: 0 0 auto; border: 0; border-radius: 6px; padding: 8px 12px; color: #fff; background: #3478e5; box-shadow: 0 3px 8px rgb(52 120 229 / 18%); cursor: pointer; font-size: 11px; }.mode-card button:hover { background: #2565cc; }.mode-card button span { margin-left: 5px; font-size: 13px; }.gallery-footnote { margin-top: 13px; color: #98a4b3; font-size: 10px; }
@media(max-width:980px) { .mode-grid { grid-template-columns: repeat(2,minmax(0,1fr)); } }
@media(max-width:760px) { .gallery-heading { align-items: flex-start; flex-direction: column; gap: 12px; }.gallery-heading h1 { font-size: 24px; }.heading-actions { flex-wrap: wrap; }.screen-count { padding: 6px 9px; font-size: 10px; }.area-selector,.mode-section { padding: 16px; }.select-row { grid-template-columns: 1fr; gap: 10px; }.mode-grid { grid-template-columns: 1fr; }.mode-card-body { align-items: flex-start; flex-direction: column; }.mode-card button { width: 100%; }.area-stat { align-items: flex-end; flex-direction: column; gap: 1px; } }
</style>
