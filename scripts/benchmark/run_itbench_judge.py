"""Run the upstream ITBench evaluator with a private DeepSeek key.
The official rubric/aggregation remain unchanged. Provider-only compatibility
settings disable thinking, bound completion tokens and SDK retries; disclose
same-model judging in the report. Run inside the upstream evaluator venv.
"""
import os,pathlib,runpy
from openai.resources.chat.completions import Completions
key=pathlib.Path(os.environ['DEEPSEEK_API_KEY_FILE']).read_text().strip()
os.environ['JUDGE_API_KEY']=key
os.environ['JUDGE_BASE_URL']='https://api.deepseek.com'
os.environ['JUDGE_MODEL']='deepseek-flash'
original=Completions.create
def compatible(self,*args,**kwargs):
 kwargs['extra_body']={**kwargs.get('extra_body',{}),'thinking':{'type':'disabled'}}
 kwargs.setdefault('max_tokens',8192)
 return original(self,*args,**kwargs)
Completions.create=compatible
runpy.run_module('itbench_evaluations',run_name='__main__')
