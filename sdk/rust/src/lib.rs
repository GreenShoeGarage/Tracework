// SPDX-License-Identifier: GPL-3.0-only
//! Blocking native HTTP/HTTPS client. Exact integers and nanoseconds stay strings.
use serde_json::{json, Map, Value};
use std::{error::Error, io::Read, net::IpAddr, time::Duration};
pub type Result<T> = std::result::Result<T, Box<dyn Error + Send + Sync>>;
pub struct Client { base: String, token: String, agent: ureq::Agent }
impl Client {
    pub fn new(base: &str, token: &str) -> Result<Self> {
        let parsed=url::Url::parse(base)?;
        let host=parsed.host_str().ok_or("Server host required")?;
        let loopback=host=="localhost" || host.trim_matches(&['[',']'][..]).parse::<IpAddr>().map(|ip|ip.is_loopback()).unwrap_or(false);
        if (parsed.scheme()!="https" && !(parsed.scheme()=="http" && loopback)) || !parsed.username().is_empty() || parsed.password().is_some() || parsed.query().is_some() || parsed.fragment().is_some() {return Err("Use HTTPS remotely or HTTP on loopback; no embedded credentials/query/fragment".into());}
        Ok(Self {base:base.trim_end_matches('/').into(), token:token.into(), agent:ureq::AgentBuilder::new().timeout(Duration::from_secs(30)).redirects(0).build()})
    }
    pub fn request(&self, method:&str, path:&str, body:Option<&Value>) -> Result<Value> {
        if !path.starts_with('/') || path.starts_with("//") || path.contains(['\r','\n']) {return Err("Relative API path required".into());}
        let url=format!("{}/api/v1{}",self.base,path);
        let mut request=self.agent.request(method,&url).set("Content-Type","application/json");
        if !self.token.is_empty(){request=request.set("Authorization",&format!("Bearer {}",self.token));}
        let response=match body {Some(v)=>request.send_json(v),None=>request.call()}?;
        if !(200..300).contains(&response.status()){return Err(format!("Unexpected HTTP {}",response.status()).into());}
        let mut bytes=Vec::new();response.into_reader().take((34<<20)+1).read_to_end(&mut bytes)?;
        if bytes.len()>34<<20{return Err("Response exceeds 34 MiB".into());}
        Ok(serde_json::from_slice(&bytes)?)
    }
    pub fn create_project(&self,name:&str)->Result<Value>{self.request("POST","/projects",Some(&json!({"name":name,"sources":[]})))}
    pub fn define_source(&self,project:&str,source:&Value)->Result<Value>{let out=self.request("POST",&format!("/projects/{}/sources",project),Some(source))?;out["sources"].as_array().and_then(|a|a.last()).cloned().ok_or_else(||"Missing source in response".into())}
    pub fn start_recording(&self,project:&str,name:&str)->Result<Value>{self.request("POST",&format!("/projects/{}/sessions",project),Some(&json!({"name":name})))}
    pub fn stop_recording(&self,session:&str)->Result<Value>{self.request("POST",&format!("/sessions/{}/stop",session),Some(&json!({})))}
    pub fn events(&self,session:&str,mut visit:impl FnMut(&Value)->Result<()>)->Result<()> {let mut after=0u64;loop{let page=self.request("GET",&format!("/sessions/{}/events?after={}",session,after),None)?;for event in page["events"].as_array().ok_or("Events array missing")?{visit(event)?;}if page["more"].as_bool()!=Some(true){return Ok(());}let next=page["next"].as_u64().ok_or("Next ordinal missing")?;if next<=after{return Err("Pagination did not advance".into());}after=next;}}
}
pub fn exact_uint64(value:u64)->Value{Value::String(value.to_string())}
pub fn exact_int64(value:i64)->Value{Value::String(value.to_string())}
/// One producer stream. Supply a fresh, durable boot identity on every producer restart.
/// Pending envelopes are retained in memory for retry; use the Go agent for disk spooling.
pub struct Source<'a>{client:&'a Client,project:String,template:Value,sequence:u64,pending:Option<Value>,exhausted:bool,pub recording:Option<String>}
impl<'a> Source<'a>{
 pub fn new(client:&'a Client,project:&str,source:&str,schema:&str,version:u32,clock:&str,boot:&str)->Result<Self>{if boot.is_empty(){return Err("Explicit unique boot identity required".into());}Ok(Self{client,project:project.into(),template:json!({"source_id":source,"schema_id":schema,"schema_version":version,"clock_domain":clock,"boot_id":boot,"sync_quality":"unsynchronized","uncertainty_ns":null}),sequence:0,pending:None,exhausted:false,recording:None})}
 pub fn emit(&mut self,values:Map<String,Value>,timestamp_ns:i64,gap:bool)->Result<Value>{if self.pending.is_some(){return Err("Unresolved send; retry the same observation first".into());}if timestamp_ns<0||self.exhausted{return Err("Invalid timestamp or exhausted sequence".into());}let mut event=self.template.clone();event["sequence"]=json!(self.sequence.to_string());event["timestamp_ns"]=json!(timestamp_ns.to_string());event["gap_before"]=json!(gap);event["values"]=Value::Object(values);self.pending=Some(event);self.retry()}
 pub fn retry(&mut self)->Result<Value>{let event=self.pending.as_ref().ok_or("No pending observation")?;let mut body=json!({"events":[event],"require_recording":true});if let Some(session)=&self.recording{body["session_id"]=json!(session)};let result=self.client.request("POST",&format!("/projects/{}/events",self.project),Some(&body))?;self.pending=None;match self.sequence.checked_add(1){Some(next)=>self.sequence=next,None=>self.exhausted=true};Ok(result)}
}
#[cfg(test)]mod tests{use super::*;#[test]fn exact_and_remote_constraints(){assert_eq!(exact_uint64(u64::MAX),json!("18446744073709551615"));assert_eq!(exact_int64(i64::MIN),json!("-9223372036854775808"));assert!(Client::new("http://192.0.2.1:8080","").is_err());assert!(Client::new("https://user:secret@example.org","").is_err());assert!(Client::new("http://127.0.0.1:8080","").is_ok());}}
