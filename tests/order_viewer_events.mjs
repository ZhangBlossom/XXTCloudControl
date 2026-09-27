// Exercise the shipped viewer JavaScript with simulated network/WebRTC APIs.
// This checks error reporting only, not real phone video or network connectivity.
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
const source=readFileSync(new URL('../server/order_assets/viewer.html',import.meta.url),'utf8').match(/<script>([\s\S]*?)<\/script>/)[1];
async function viewer({expired=false,unauthorized=false,signalFails=false}={}){
  const calls=[],listeners={},peers=[],elements={};
  const context=vm.createContext({URLSearchParams,Date,Promise,Error,Math,JSON,
    location:{hash:'#order=test&token=private-token',pathname:'/order-viewer'},history:{replaceState(){}},
    document:{getElementById(id){return elements[id]??={};}},
    window:{addEventListener(name,fn){listeners[name]=fn;}},
    setInterval(){},setTimeout(){},confirm(){return false;},
    fetch:async(url,options)=>{
      calls.push({url,body:options.body});
      if(url.endsWith('/failure'))return {ok:true,status:204};
      if(unauthorized)return {ok:false,status:401};
      if(url.endsWith('/status'))return {ok:true,status:200,json:async()=>({expires_at:new Date(Date.now()+(expired?-1000:360000)).toISOString(),remaining:1})};
      if(signalFails)throw new Error('network failed');
      if(url.endsWith('/signal/start'))return {ok:true,status:200,json:async()=>({type:'offer',sdp:'test'})};
      if(url.endsWith('/signal/poll'))return new Promise(()=>{});
      return {ok:true,status:200,json:async()=>({})};
    },
    RTCPeerConnection:class{
      constructor(){peers.push(this);this.connectionState='connected';}
      async setRemoteDescription(){} async createAnswer(){return {sdp:'answer'};} async setLocalDescription(){}
      close(){this.connectionState='closed';this.onconnectionstatechange?.();}
    }
  });
  vm.runInContext(source,context);
  const flush=async()=>{for(let i=0;i<10;i++)await new Promise(resolve=>setImmediate(resolve));};
  await flush();
  return {calls,listeners,peers,flush,failures:()=>calls.filter(c=>c.url.endsWith('/failure')).map(c=>JSON.parse(c.body).reason)};
}
let v=await viewer();v.listeners.offline();await v.flush();v.listeners.offline();await v.flush();
assert.deepEqual(v.failures(),['browser_offline']);
v=await viewer();v.peers[0].connectionState='failed';v.peers[0].onconnectionstatechange();await v.flush();
assert.deepEqual(v.failures(),['connection_failed']);
v=await viewer({signalFails:true});assert.deepEqual(v.failures(),['signal_failed']);
v=await viewer({unauthorized:true});assert.deepEqual(v.failures(),[]);
v=await viewer({expired:true});assert.deepEqual(v.failures(),[]);
v=await viewer();v.listeners.pagehide();await v.flush();assert.deepEqual(v.failures(),[]);
console.log('PASS: shipped viewer reports offline/connection/signal failure once; expiry, invalid tokens and page exit are not reported as network failures (simulated APIs).');
