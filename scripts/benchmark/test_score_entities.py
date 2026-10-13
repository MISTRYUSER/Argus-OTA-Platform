import copy
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest
from score_entities import score
class EntityScoreTests(unittest.TestCase):
 def setUp(self):self.gt={'groups':[{'id':'p','kind':'Pod','filter':['worker-.*'],'namespace':'app','root_cause':True},{'id':'s','kind':'Service','filter':['worker'],'namespace':'app'}],'aliases':[['p','s']]}
 def test_alias_and_false_symptom(self):
  got=score(self.gt,{'entities':[{'name':'app/Service/worker','contributing_factor':False},{'name':'app/Pod/worker-123','contributing_factor':True}]});self.assertEqual(got['hit_at_1'],1);self.assertEqual(got['candidate_count'],1)
 def test_wrong_kind_and_namespace_do_not_match(self):
  for name in ['app/Deployment/worker-123','other/Pod/worker-123']:
   self.assertEqual(score(self.gt,{'entities':[{'name':name,'contributing_factor':True}]})['hit_at_1'],0)
 def test_empty_failure_is_zero(self):self.assertEqual(score(self.gt,{'entities':[]})['f1'],0)
 def test_invalid_regex_unknown_even_for_empty_prediction_without_rewriting_gold(self):
  self.gt['groups'][0]['filter']=['*.*'];original=copy.deepcopy(self.gt)
  for entities in [[],[{'name':'app/Pod/worker-123','contributing_factor':True}]]:
   got=score(self.gt,{'entities':entities});self.assertIs(got['score_valid'],False)
   for field in ['hit_at_1','precision','recall','f1']:self.assertIsNone(got[field])
   self.assertEqual(got['errors'][0]['group_id'],'p');self.assertEqual(got['errors'][0]['filter'],'*.*')
   self.assertEqual(self.gt,original)
 def test_all_filters_prevalidated_even_if_an_earlier_pattern_matches(self):
  self.gt['groups'][0]['filter']=['worker-.*','[']
  got=score(self.gt,{'entities':[{'name':'app/Pod/worker-123','contributing_factor':True}]})
  self.assertIs(got['score_valid'],False);self.assertEqual(got['errors'][0]['filter'],'[')
 def test_cli_continues_other_cases_retains_unknown_rows_and_macro_coverage(self):
  with tempfile.TemporaryDirectory() as tmp:
   root=pathlib.Path(tmp);gt=root/'gt';pred=root/'pred';out=root/'scores.json'
   for sid in ['1','38']:
    directory=gt/f'Scenario-{sid}';directory.mkdir(parents=True)
    (directory/'ground_truth.yaml').write_text('groups:\n  - id: root\n    kind: Pod\n    namespace: app\n    root_cause: true\n    filter: ["'+('worker-.*' if sid=='1' else '*.*')+'"]\n')
    for trial in range(3):
     directory=pred/f'scenario-{sid}'/str(trial)/'outputs';directory.mkdir(parents=True)
     (directory/'agent_output.json').write_text(json.dumps({'entities':[{'name':'app/Pod/worker-1','contributing_factor':True}]}))
   process=subprocess.run([sys.executable,str(pathlib.Path(__file__).with_name('score_entities.py')),'--ground-truth',str(gt),'--predictions',str(pred),'--out',str(out)],capture_output=True,text=True)
   self.assertEqual(process.returncode,0,process.stderr);report=json.loads(out.read_text())
   self.assertEqual(report['n_runs'],6);self.assertEqual(report['n_scored_runs'],3)
   self.assertEqual(report['n_scored_incidents'],1);self.assertEqual(report['macro_metrics']['f1'],1)
   self.assertIsNone(report['macro_metrics_all_incidents']['f1'])
   unknown=[row for row in report['rows'] if not row['score_valid']]
   self.assertEqual(len(unknown),3);self.assertEqual(unknown[0]['errors'][0]['filter'],'*.*')
if __name__=='__main__':unittest.main()
