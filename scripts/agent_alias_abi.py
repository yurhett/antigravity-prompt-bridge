#!/usr/bin/env python3
"""Native ABI regressions for the exact field whitelist; only synthetic data."""
import base64,copy,json,sys
from pathlib import Path
from abi_support import Plugin,NATIVE_IDENTITY

def enc(v):return base64.b64encode(json.dumps(v).encode()).decode()
def main():
 p=Plugin(Path(sys.argv[1]).resolve());p.provider='antigravity'
 source='tools.opencode.read tools.antigravity.read Continue.dev Refact.ai /tmp/opencode /tmp/antigravity'
 try:
  p.configure('agent_alias: true\nantigravity_bridge_system_prompt: false\n')
  payload={'system':'OpenCode','name':'opencode_probe','opencode_key':'opencode','source':source,'namespace':'opencode','path':'/tmp/opencode'}
  schema={'properties':{'opencode_key':{'description':'Use `opencode_probe` in OpenCode.','default':{'description':'OpenCode'},'enum':['OpenCode']}}}
  request={'systemInstruction':{'parts':[{'text':'You are OpenCode. Use opencode_probe. Files: /tmp/opencode. Plugin: /home/u/.config/opencode/plugins/opencode.ts'}]},'tools':[{'functionDeclarations':[{'name':'opencode_probe','description':'OpenCode probe. '+source,'parametersJsonSchema':schema}]}],'contents':[{'role':'user','parts':[{'text':source},{'functionResponse':{'name':'opencode_probe','response':payload}}]},{'role':'model','parts':[{'functionCall':{'id':'keep-id','name':'opencode_probe','args':payload},'thoughtSignature':'keep-signature'}]}]}
  result=p.call('request.intercept_after',{'RequestID':'whitelist','SourceFormat':'gemini','Stream':True,'Metadata':{'selected_auth_index':'mock'},'Body':enc(request)})
  assert not result.get('Terminate'),result
  actual=json.loads(base64.b64decode(result['Body']));expected=copy.deepcopy(request)
  expected['systemInstruction']['parts'][0]['text']=NATIVE_IDENTITY+' Use antigravity_probe. Files: /tmp/opencode. Plugin: /home/u/.config/opencode/plugins/opencode.ts'
  tool = expected['tools'][0]['functionDeclarations'][0]
  tool['name']='antigravity_probe'
  tool['description']='Antigravity probe. '+source
  tool['parametersJsonSchema']['properties']['opencode_key']['description']='Use `antigravity_probe` in Antigravity.'
  expected['contents'][0]['parts'][1]['functionResponse']['name']='antigravity_probe'
  expected['contents'][1]['parts'][0]['functionCall']['name']='antigravity_probe'
  assert actual==expected,actual
  response={'candidates':[{'content':{'parts':[{'functionCall':{'name':'antigravity_probe','args':payload},'thoughtSignature':'keep-signature'},{'text':source}]}}]}
  result=p.call('response.intercept_after',{'RequestID':'whitelist','Body':enc(response)})
  expected=copy.deepcopy(response);expected['candidates'][0]['content']['parts'][0]['functionCall']['name']='opencode_probe'
  assert json.loads(base64.b64decode(result['Body']))==expected
  wire=('data: '+json.dumps(response)+'\r\n\r\ndata: [DONE]\n\n').encode();output=bytearray()
  for i in range(0,len(wire),3):
   result=p.call('response.intercept_stream_chunk',{'RequestID':'whitelist','ChunkIndex':i,'Body':base64.b64encode(wire[i:i+3]).decode()})
   if not result.get('DropChunk'):output.extend(base64.b64decode(result.get('Body','')))
  payloads=[line[6:] for line in output.decode().splitlines() if line.startswith('data: ') and line!='data: [DONE]'];assert json.loads(payloads[0])==expected
  p.call('request.complete',{'RequestID':'whitelist'})
  result=p.call('response.intercept_after',{'RequestID':'whitelist','Body':enc(response)});assert not result.get('Body')
 finally:p.close()
 print(json.dumps({'passed':True,'strict_field_whitelist':True,'tool_names_round_trip':True,'schemas_and_arguments_unchanged':True,'tool_results_and_source_unchanged':True,'plugin_paths_unchanged':True,'system_tool_references_aliased':True,'fragmented_sse':True,'completion_cleanup':True}))
if __name__=='__main__':main()
