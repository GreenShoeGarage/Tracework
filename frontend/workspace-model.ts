// SPDX-License-Identifier: GPL-3.0-only
export interface FieldShape {id:string;name:string;type:string;unit:string;enum?:string[]}
export interface SchemaShape {id:string;name:string;version:number;fields:FieldShape[]}
export interface SourceShape {id:string;name:string;clock_domain:string;stale_ms:number;synthetic:boolean;events:SchemaShape[]}
export interface SignalBinding {key:string;src:SourceShape;sc:SchemaShape;f:FieldShape}
export interface Observation {id:string;source_id:string;schema_id:string;schema_version:number;boot_id:string;sequence:string;timestamp_ns:string;received_ns:string;values:Record<string,number|string|boolean|null>;ordinal:number;gap_before:boolean;flags?:string[];message?:string;level?:string}
export interface SignalRef {source_id:string;schema_id:string;field_id:string;version:number;source_name:string;schema_name:string;field_name:string;type:string;unit:string;enum:string[]}
export type PanelKind='plot'|'value'|'table'|'raw'|'state'|'log';
export interface PanelSpec {id:string;kind:PanelKind;title:string;height:number;span:number;binding_mode:'selection'|'pinned'|'portable';bindings:SignalRef[];scope:'all'|'bindings';filter:string;level:string;sort:'signal'|'source'|'value'|'time';descending:boolean;decimals:number;palette:'instrument'|'blue'|'warm';line:'linear'|'step'|'points';y_min:string;y_max:string}
export interface LayoutTab {id:string;name:string;columns:number;panels:PanelSpec[]}
export interface Workspace {format:'tracework.workspace.v1';active_tab:string;tabs:LayoutTab[]}
export interface WorkspaceState {revision:number;workspace:Workspace|null}
export interface ViewContext {bindings:SignalBinding[];selected:Set<string>;events:Observation[];cursor:number;recorded:boolean;online:boolean;follow:boolean;sourceStates:Record<string,string>;scopeKey:string}
export const panelNames:Record<PanelKind,string>={plot:'Plot',value:'Values',table:'Signal table',raw:'Raw events',state:'State timeline',log:'Logs'};
export const freshID=()=> 'view_'+Array.from(crypto.getRandomValues(new Uint8Array(16)),b=>b.toString(16).padStart(2,'0')).join('');
export const clone=<T>(v:T):T=>JSON.parse(JSON.stringify(v));
export function newPanel(kind:PanelKind,id=freshID()):PanelSpec{return {id,kind,title:panelNames[kind],height:kind==='value'?220:400,span:2,binding_mode:'selection',bindings:[],scope:kind==='raw'||kind==='log'?'all':'bindings',filter:'',level:'',sort:'signal',descending:false,decimals:-1,palette:'instrument',line:'linear',y_min:'',y_max:''}}
export function defaultWorkspace():Workspace{return {format:'tracework.workspace.v1',active_tab:'plot',tabs:[{id:'plot',name:'Signals & plot',columns:2,panels:[newPanel('value','values_main'),newPanel('plot','plot_main')]},{id:'raw',name:'Event ledger',columns:1,panels:[newPanel('raw','raw_main')]},{id:'logs',name:'Logs',columns:1,panels:[newPanel('log','logs_main')]}]}}
export function reference(b:SignalBinding):SignalRef{return {source_id:b.src.id,schema_id:b.sc.id,field_id:b.f.id,version:b.sc.version,source_name:b.src.name,schema_name:b.sc.name,field_name:b.f.name,type:b.f.type,unit:b.f.unit,enum:b.f.enum??[]}}
export function resolvePanel(p:PanelSpec,c:ViewContext):{bindings:SignalBinding[];issues:string[]}{
 if(p.binding_mode==='selection')return {bindings:c.bindings.filter(b=>c.selected.has(b.key)).slice(0,8),issues:[]};
 const bindings:SignalBinding[]=[],issues:string[]=[];
 for(const r of p.bindings){
  const candidates=c.bindings.filter(b=>p.binding_mode==='pinned'?b.src.id===r.source_id&&b.sc.id===r.schema_id&&b.sc.version===r.version&&b.f.id===r.field_id:b.src.name===r.source_name&&b.sc.name===r.schema_name&&b.sc.version===r.version&&b.f.name===r.field_name);
  const label=`${r.source_name} / ${r.schema_name} v${r.version} / ${r.field_name}`;
  if(candidates.length!==1){issues.push(`${label}: ${candidates.length?'ambiguous path':'not found'}`);continue}
  const b=candidates[0];if(b.f.type!==r.type||b.f.unit!==r.unit||JSON.stringify(b.f.enum??[])!==JSON.stringify(r.enum??[])){issues.push(`${label}: type, unit or enum changed`);continue}
  bindings.push(b)
 }
 return {bindings,issues}
}
export function sameSeries(e:Observation,b:SignalBinding){return e.source_id===b.src.id&&e.schema_id===b.sc.id&&e.schema_version===b.sc.version}
export function eventAt(c:ViewContext,b:SignalBinding){for(let i=Math.min(c.cursor,c.events.length-1);i>=0;i--)if(sameSeries(c.events[i],b))return c.events[i]}
export function statusAt(c:ViewContext,b:SignalBinding,e?:Observation){if(!e)return 'No observations on this page';if(e.values[b.f.name]===null)return 'Unavailable (null)';if(c.recorded)return 'Recorded observation';if(!c.online||c.sourceStates[b.src.id]!=='receiving'||Date.now()-Number(BigInt(e.received_ns)/1000000n)>b.src.stale_ms)return 'Stale / disconnected';return c.follow?'Receiving':'Paused view'}
export function formatValue(v:unknown,decimals=-1){if(v===null||v===undefined)return 'Unavailable';if(typeof v==='number')return decimals<0?Number(v.toPrecision(7)).toString():v.toFixed(decimals);return String(v)}
export function isGap(previous:Observation|undefined,next:Observation,b:SignalBinding){return !previous||next.gap_before||previous.boot_id!==next.boot_id||BigInt(next.received_ns)<BigInt(previous.received_ns)||BigInt(next.received_ns)-BigInt(previous.received_ns)>BigInt(b.src.stale_ms)*1000000n}
export function compareValues(a:unknown,b:unknown,typeA:string,typeB:string):number{
 if(a===null||a===undefined)return b===null||b===undefined?0:1;if(b===null||b===undefined)return -1;
 if(['int64','uint64'].includes(typeA)&&['int64','uint64'].includes(typeB)){const x=BigInt(String(a)),y=BigInt(String(b));return x<y?-1:x>y?1:0}
 if(typeA==='float64'&&typeB==='float64')return Number(a)-Number(b);
 return String(a).localeCompare(String(b),undefined,{numeric:false})
}
export function extent(events:Observation[]):[bigint,bigint]{if(!events.length)return [0n,1n];let a=BigInt(events[0].received_ns),b=a;for(const e of events){const t=BigInt(e.received_ns);if(t<a)a=t;if(t>b)b=t}return [a,b>a?b:a+1n]}
export function boundedWindow(a:bigint,b:bigint,full:[bigint,bigint]):[bigint,bigint]{let span=b-a;if(span<1n)span=1n;const total=full[1]-full[0];if(span>=total)return [...full];if(a<full[0])a=full[0];if(a+span>full[1])a=full[1]-span;return [a,a+span]}
