// SPDX-License-Identifier: GPL-3.0-only
import {PanelSpec,ViewContext,SignalBinding,Observation,eventAt,statusAt,formatValue,sameSeries,isGap,compareValues,extent} from './workspace-model.js';
export const escapeHTML=(v:unknown)=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]!));
const e=escapeHTML;
const empty=(message:string)=>`<div class="panel-empty">${e(message)}</div>`;
function filtered(p:PanelSpec,c:ViewContext,bs:SignalBinding[]){const q=p.filter.toLowerCase();return c.events.slice(0,c.cursor+1).filter(ev=>(p.scope==='all'||bs.some(b=>sameSeries(ev,b)))&&(!q||JSON.stringify(ev).toLowerCase().includes(q))&&(p.kind!=='log'||!p.level||ev.level===p.level))}
export function panelHTML(p:PanelSpec,c:ViewContext,bs:SignalBinding[],window:[bigint,bigint],width:number):string{
 if(p.kind==='value')return bs.length?bs.map(b=>{const ev=eventAt(c,b);return `<div class="readout"><div class="name">${e(b.src.name)} / ${e(b.sc.name)} v${b.sc.version} / ${e(b.f.name)}</div><div class="value"><span>${e(formatValue(ev?.values[b.f.name],p.decimals))}</span><small>${e(b.f.unit)}</small></div><div class="state">${e(statusAt(c,b,ev))}${b.src.synthetic?' · SYNTHETIC':''}</div></div>`}).join(''):empty('Select signals in the browser, or configure this panel’s bindings.');
 if(p.kind==='table'){
  let rows=bs.map(b=>({b,ev:eventAt(c,b)})).filter(r=>`${r.b.src.name} ${r.b.f.name} ${formatValue(r.ev?.values[r.b.f.name])} ${r.b.f.unit} ${statusAt(c,r.b,r.ev)}`.toLowerCase().includes(p.filter.toLowerCase()));
  rows.sort((a,b)=>{let n=0;if(p.sort==='source')n=a.b.src.name.localeCompare(b.b.src.name);else if(p.sort==='value')n=compareValues(a.ev?.values[a.b.f.name],b.ev?.values[b.b.f.name],a.b.f.type,b.b.f.type);else if(p.sort==='time'){const x=BigInt(a.ev?.timestamp_ns??'0'),y=BigInt(b.ev?.timestamp_ns??'0');n=x<y?-1:x>y?1:0}else n=a.b.f.name.localeCompare(b.b.f.name);return p.descending?-n:n});
  return `<div class="tablewrap"><table class="signal-table"><thead><tr><th>Source / event</th><th>Signal</th><th>Exact value</th><th>Unit</th><th>Source time · ns</th><th>State</th></tr></thead><tbody>${rows.map(({b,ev})=>`<tr><td>${e(b.src.name)} / ${e(b.sc.name)} v${b.sc.version}</td><td>${e(b.f.name)}</td><td>${ev?`<button data-observation="${ev.ordinal}">${e(ev.values[b.f.name]===null?'null':ev.values[b.f.name])}</button>`:'Unavailable'}</td><td>${e(b.f.unit)}</td><td>${e(ev?.timestamp_ns??'—')}</td><td>${e(statusAt(c,b,ev))}</td></tr>`).join('')}</tbody></table></div>${rows.length?'':empty('No matching signals.')}<p class="panel-note">Snapshot at the shared cursor. Integer sorting uses exact integers; mixed numeric types sort as text.</p>`
 }
 if(p.kind==='raw'){
  const all=filtered(p,c,bs),rows=all.slice(-100);return `<div class="tablewrap"><table><thead><tr><th>Sequence</th><th>Source / event</th><th>Source timestamp · ns</th><th>Original payload</th><th>Coverage</th></tr></thead><tbody ${p.id==='raw_main'?'id="eventRows"':''}>${rows.map(ev=>{const b=c.bindings.find(b=>sameSeries(ev,b));return `<tr class="${ev===c.events[c.cursor]?'selected':''}"><td><button data-observation="${ev.ordinal}">${e(ev.sequence)}</button></td><td>${e(b?.src.name??ev.source_id)} / ${e(b?.sc.name??ev.schema_id)} v${ev.schema_version}</td><td>${e(ev.timestamp_ns)}</td><td>${e(JSON.stringify(ev.values))}</td><td>${ev.gap_before?'Gap / discontinuity':'Accepted'}</td></tr>`}).join('')}</tbody></table></div><p class="panel-note">${rows.length} of ${all.length} matching events on this page, ending at the cursor. Select a row for its full envelope.</p><details><summary>Selected event · complete original envelope</summary><pre>${e(c.events[c.cursor]?JSON.stringify(c.events[c.cursor],null,2):'No selected event.')}</pre></details>`
 }
 if(p.kind==='log'){
  const rows=filtered(p,c,bs).filter(ev=>ev.message).slice(-200).reverse();return rows.map(ev=>{const b=c.bindings.find(b=>sameSeries(ev,b));return `<div class="logrow"><span class="flag">${e(ev.level??'info')}</span><div class="logbody">${e(ev.message)}<br><small>${e(b?.src.name??ev.source_id)} · ${e(ev.timestamp_ns)} ns</small></div><button data-observation="${ev.ordinal}">Inspect ↗</button></div>`}).join('')||empty('No matching log messages at or before the shared cursor on this page.')
 }
 if(!c.events.length)return empty('Waiting for observations. Capture live data, open a recording, or import a trace.');
 const signals=bs.filter(b=>p.kind==='state'?['bool','enum','string'].includes(b.f.type):['float64','int64','uint64'].includes(b.f.type));
 if(!signals.length)return empty(p.kind==='state'?'Choose bool, enum or string signals for a state timeline.':'Choose float64, int64 or uint64 signals for a numeric plot.');
 const full=extent(c.events),w=Math.max(240,width),left=57,right=w-16,plotW=right-left,[start,end]=window,span=end-start;
 const x=(ev:Observation)=>left+Number(BigInt(ev.received_ns)-start)/Number(span)*plotW;
 const unit=Number(span)<1e3?'ns':Number(span)<1e6?'µs':Number(span)<1e9?'ms':'s',div=unit==='ns'?1:unit==='µs'?1e3:unit==='ms'?1e6:1e9;
 const label=(t:bigint)=>`${(Number(t-full[0])/div).toPrecision(4)} ${unit}`;
 const theme=document.documentElement.dataset.theme??'dark';
 const palettes=theme==='light'?{instrument:['#426414','#057d7e','#a45316','#7754b1'],blue:['#075d8f','#493897','#166874','#6c4590'],warm:['#a13b12','#895516','#862257','#805915']}:{instrument:['#cef278','#7fd9d1','#ffbc83','#c3b5f4'],blue:['#80cfff','#bbb5ff','#80e6e0','#d5a8ff'],warm:['#ffbd87','#ffdb80','#ffa8cb','#e5d6a0']};
 return signals.map((b,index)=>{
  const series=c.events.filter(ev=>sameSeries(ev,b));const visible=series.filter(ev=>BigInt(ev.received_ns)>=start&&BigInt(ev.received_ns)<=end);
  const color=theme==='contrast'?['#ffff00','#00ffff','#ffffff'][index%3]:palettes[p.palette][index%4];
  const clip=`clip_${p.id}_${index}`,cursor=c.events[c.cursor],cx=cursor?x(cursor):-1;
  let drawing='',note='',key='';
  if(p.kind==='state'){
   const labels=[...new Set(series.map(ev=>ev.values[b.f.name]).filter(v=>v!==null).map(String))];
   const stateColor=(ix:number)=>(theme==='light'?['#426414','#057d7e','#7754b1','#a45316']:['#cef278','#7fd9d1','#c3b5f4','#ffbc83'])[ix%4];key='<div class="state-key">'+labels.slice(0,12).map((label,i)=>`<span title="${e(label)}"><i style="background:${stateColor(i)}"></i>${e(label||'(empty string)')}</span>`).join('')+(labels.length>12?`<small>12 of ${labels.length} labels · inspect for exact state</small>`:'')+'</div>';
   drawing=series.map((ev,i)=>{const value=ev.values[b.f.name];if(value===null)return '';const next=series[i+1];const t=BigInt(ev.received_ns);if(t>end||next&&BigInt(next.received_ns)<start)return '';const a=x(ev);const contiguous=next&&!isGap(ev,next,b)&&next.values[b.f.name]!==null;const finish=contiguous?x(next):a+2;const aa=Math.max(left,a),bb=Math.min(right,finish);if(bb<aa)return '';const ix=labels.indexOf(String(value));const fill=stateColor(ix);return `<g data-observation="${ev.ordinal}"><title>${e(String(value))} · ${e(ev.received_ns)} ns${contiguous?' to '+e(next.received_ns)+' ns':' (observation only)'}</title><rect x="${aa}" y="35" width="${Math.max(2,bb-aa)}" height="52" fill="${fill}" opacity=".24"/><path d="M${aa} 35V87" stroke="${fill}"/>${bb-aa>55?`<text x="${aa+4}" y="65" fill="currentColor">${e(String(value).slice(0,Math.max(3,Math.floor((bb-aa)/8)-1)))}</text>`:''}</g>`}).join('');
   note='Observed state intervals only; gaps, nulls and restarts stay unfilled.';
  }else{
   const base=series.find(ev=>ev.values[b.f.name]!==null)?.values[b.f.name];const offset=b.f.type!=='float64'&&base!==undefined?BigInt(String(base)):null;const number=(v:unknown)=>offset===null?Number(v):Number(BigInt(String(v))-offset);
   const nums=visible.filter(ev=>ev.values[b.f.name]!==null).map(ev=>number(ev.values[b.f.name])).filter(Number.isFinite);
   if(!nums.length)return `<div class="plotlane"><div class="legend">${e(b.src.name)} / ${e(b.f.name)}</div>${empty('No numeric samples in this zoom window.')}</div>`;
   let lo=Math.min(...nums),hi=Math.max(...nums);const pad=hi*.05-lo*.05||Math.max(1,Math.abs(hi)*.05);lo=Math.max(-Number.MAX_VALUE,lo-pad);hi=Math.min(Number.MAX_VALUE,hi+pad);if(p.y_min!==''&&p.y_max!==''){lo=Number(p.y_min);hi=Number(p.y_max)}
   const scale=Math.max(Math.abs(lo),Math.abs(hi),1),denom=hi/scale-lo/scale;
   const y=(n:number)=>130-(n/scale-lo/scale)/denom*110;let path='',dots='',previous:Observation|undefined,previousY=0;
   for(const ev of series){const t=BigInt(ev.received_ns);if(t<start||t>end||ev.values[b.f.name]===null){previous=undefined;continue}const n=number(ev.values[b.f.name]),yy=y(n);if(!Number.isFinite(yy)||Math.abs(yy)>1e7){previous=undefined;continue}const xx=x(ev),gap=isGap(previous,ev,b);if(p.line!=='points'){path+=(gap?'M'+xx+','+yy:p.line==='step'?'L'+xx+','+previousY+'L'+xx+','+yy:'L'+xx+','+yy)+' '};if(visible.length<=400||p.line==='points')dots+=`<circle cx="${xx}" cy="${yy}" r="2"/>`;previous=ev;previousY=yy}
   drawing=`<text x="3" y="24">${e(hi.toPrecision(4))}</text><text x="3" y="79">${e((lo/2+hi/2).toPrecision(4))}</text><text x="3" y="133">${e(lo.toPrecision(4))}</text><g clip-path="url(#${clip})"><path d="${path}" stroke="${color}" fill="none" stroke-width="2"/><g fill="${color}">${dots}</g></g>`;
   note=offset!==null?`Δ from ${offset.toString()} · exact values in inspector`:'Separate scale · '+(b.f.unit||'no unit');
  }
  const height=p.kind==='state'?120:164;
  return `<div class="plotlane"><div class="legend"><span>${e(b.src.name)} / <b>${e(b.f.name)}</b> ${e(b.f.unit)}</span><span>${e(note)}</span></div>${key}<svg data-chart="true" data-source="${e(b.src.id)}" data-schema="${e(b.sc.id)}" data-version="${b.sc.version}" data-from="${start}" data-to="${end}" data-width="${w}" data-left="${left}" data-right="${right}" viewBox="0 0 ${w} ${height}" role="img" aria-label="${e(b.f.name)} over receive time"><defs><clipPath id="${clip}"><rect x="${left}" y="15" width="${plotW}" height="120"/></clipPath></defs><path class="gridline" d="M${left} 20H${right} M${left} 75H${right} M${left} 130H${right} ${[0,.25,.5,.75,1].map(n=>`M${left+n*plotW} 20V${height-28}`).join(' ')}"/>${drawing}<text x="${left}" y="${height-7}">${e(label(start))}</text><text x="${right-90}" y="${height-7}">${e(label(end))}</text>${cx>=left&&cx<=right?`<path d="M${cx} 15V${height-25}" stroke="${color}" stroke-dasharray="4 4"/>`:''}</svg></div>`
 }).join('')
}
