#!/usr/bin/env python3
"""OpenCode adapter across the four native ABIs; synthetic requests only."""
import base64,copy,json,sys
from pathlib import Path
from abi_support import Plugin

def enc(v):return base64.b64encode(json.dumps(v).encode()).decode()
def decode(r):return json.loads(base64.b64decode(r['Body']))
SYSTEM='You are an AI agent running in OpenCode, a coding agent harness.\n# Code Mode\nThe tools listed below, and `search`, cannot be called directly: they only work as JavaScript inside the `execute` tool.\n## Code Mode tools (call only inside `execute`)\n- opencode (1 tool, 1 shown)\n  - tools.opencode.worktree_list(): Promise<{}>\n'
SCHEMA={'type':'object','properties':{'code':{'type':'string'}},'required':['code']}
ORIGINAL='return await tools.opencode.worktree_list({content:"OpenCode tools.antigravity.worktree_list"});'
UPSTREAM=ORIGINAL.replace('tools.opencode.worktree_list','tools.antigravity.worktree_list')
def codes(v):
 if isinstance(v,str):
  try:return codes(json.loads(v))
  except (ValueError,TypeError):return []
 if isinstance(v,dict):return ([v['code']] if isinstance(v.get('code'),str) else [])+sum((codes(x) for k,x in v.items() if k!='code'),[])
 if isinstance(v,list):return sum((codes(x) for x in v),[])
 return []

def main():
 p=Plugin(Path(sys.argv[1]).resolve());p.provider='antigravity';reports=[]
 try:
  for fmt in ['gemini','openai','openai-response','claude']:
   p.configure('agent_alias: true\nantigravity_bridge_system_prompt: false\n')
   tool={'name':'execute','description':'Run JavaScript in a confined Code Mode runtime to script tool calls.'}
   request={'system':SYSTEM};args=json.dumps({'code':UPSTREAM})
   if fmt=='gemini':request['tools']=[{'functionDeclarations':[{**tool,'parametersJsonSchema':SCHEMA}]}]
   elif fmt=='openai':request['tools']=[{'type':'function','function':{**tool,'parameters':SCHEMA}}]
   elif fmt=='openai-response':request['tools']=[{'type':'function',**tool,'parameters':SCHEMA}]
   else:request['tools']=[{**tool,'input_schema':SCHEMA}]
   r=p.call('request.intercept_after',{'RequestID':fmt,'SourceFormat':fmt,'Stream':True,'Metadata':{'selected_auth_index':'mock'},'Body':enc(request)})
   assert not r.get('Terminate'),r
   actual=decode(r);assert 'tools.opencode.' not in actual['system'];assert actual['tools']==request['tools']
   if fmt=='gemini':
    response={'candidates':[{'content':{'parts':[{'functionCall':{'name':'execute','id':'keep','args':{'code':UPSTREAM}},'thoughtSignature':'keep-signature'}]}}]};events=[response]
   elif fmt=='openai':
    response={'choices':[{'message':{'tool_calls':[{'id':'keep','type':'function','function':{'name':'execute','arguments':args}}]}}]}
    events=[{'choices':[{'index':0,'delta':{'tool_calls':[{'index':0,'id':'keep','type':'function','function':{'name':'execute','arguments':args[:9]}}]}}]}, {'choices':[{'index':0,'delta':{'tool_calls':[{'index':0,'function':{'arguments':args[9:]}}]}}]}, {'choices':[{'index':0,'delta':{},'finish_reason':'tool_calls'}]}]
   elif fmt=='openai-response':
    item={'type':'function_call','id':'item','call_id':'keep','name':'execute','arguments':args};response={'output':[item]}
    events=[{'type':'response.output_item.added','item':{**item,'arguments':''}},{'type':'response.function_call_arguments.delta','item_id':'item','delta':args},{'type':'response.function_call_arguments.done','item_id':'item','arguments':args},{'type':'response.output_item.done','item':item},{'type':'response.completed','response':{'output':[item]}}]
   else:
    response={'content':[{'type':'tool_use','id':'keep','name':'execute','input':{'code':UPSTREAM}}]}
    events=[{'type':'content_block_start','index':0,'content_block':{'type':'tool_use','id':'keep','name':'execute','input':{}}},{'type':'content_block_delta','index':0,'delta':{'type':'input_json_delta','partial_json':args}},{'type':'content_block_stop','index':0},{'type':'message_stop'}]
   r=p.call('response.intercept_after',{'RequestID':fmt,'Body':enc(response)});actual=decode(r)
   assert ORIGINAL in codes(actual),actual
   wire=(''.join('data: '+json.dumps(e)+'\n\n' for e in events)+'data: [DONE]\n\n').encode();output=bytearray()
   for i in range(0,len(wire),7):
    r=p.call('response.intercept_stream_chunk',{'RequestID':fmt,'ChunkIndex':i,'Body':base64.b64encode(wire[i:i+7]).decode()})
    if not r.get('DropChunk'):output.extend(base64.b64decode(r.get('Body','')))
   assert b'agent_alias_restore_failed' not in output,output
   payloads=[json.loads(line[6:]) for line in output.decode().splitlines() if line.startswith('data: ') and line!='data: [DONE]']
   # Chat arguments and Anthropic partial_json remain nested JSON strings.
   assert ORIGINAL in codes(payloads),output
   p.call('request.complete',{'RequestID':fmt});reports.append({'protocol':fmt,'system_names_aliased':True,'nonstream':True,'fragmented_stream':True,'ordinary_string_preserved':True})
  # Keyword identity rewriting is independent of the alias switch; disable it to measure only the alias and adapter.
  p.configure('agent_alias: false\nunidentified: legacy\nuse_default_keywords: false\nantigravity_bridge_system_prompt: false\n',heuristic=False)
  assert p.transform(request)==request
 finally:p.close()
 print(json.dumps({'passed':True,'protocols':reports,'unified_switch':True}))
if __name__=='__main__':main()
