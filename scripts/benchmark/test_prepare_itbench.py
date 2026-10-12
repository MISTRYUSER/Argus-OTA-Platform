import csv,json,pathlib,tempfile,unittest
from prepare_itbench import records
class AdapterTests(unittest.TestCase):
 def make(self,body):
  d=pathlib.Path(tempfile.mkdtemp());self.addCleanup(__import__('shutil').rmtree,d)
  (d/'alerts').mkdir();(d/'ground_truth.yaml').write_text('SECRET_LABEL_MUST_NOT_LEAK')
  for name,rows in [('k8s_events_raw.tsv',[]),('k8s_objects_raw.tsv',body)]:
   with open(d/name,'w') as f:
    w=csv.DictWriter(f,fieldnames=['Body','TimestampTime'],delimiter='\t');w.writeheader();w.writerows(rows)
  return d
 def test_large_object_and_repeated_chunks_are_preserved(self):
  obj={'kind':'Pod','metadata':{'namespace':'otel-demo','name':'fixture'},'status':{'message':'X'*150000}}
  d=self.make([{'Body':json.dumps(obj),'TimestampTime':'2025-01-01'}]);lines=records(d)
  self.assertGreater(len(lines),400);reassembled=''.join(x.split(' ',2)[2] for x in lines)
  self.assertEqual(json.loads(reassembled)['object']['status']['message'],'X'*150000)
  self.assertNotIn('SECRET_LABEL',reassembled);self.assertTrue(all(len(x)<=400 for x in lines))
 def test_latest_snapshot_does_not_depend_on_input_order(self):
  def obj(message):return json.dumps({'kind':'Pod','metadata':{'namespace':'otel-demo','name':'fixture'},'status':{'message':message}})
  d=self.make([{'Body':obj('new'),'TimestampTime':'2025-02-01'},{'Body':obj('old'),'TimestampTime':'2025-01-01'}]);joined=''.join(x.split(' ',2)[2] for x in records(d));self.assertEqual(json.loads(joined)['object']['status']['message'],'new')
if __name__=='__main__':unittest.main()
