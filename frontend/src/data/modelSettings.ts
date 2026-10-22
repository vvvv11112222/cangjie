import { requestJson } from './http';
import { asBoolean, asInteger, asRecord, asString } from './guards';
import { ApiError } from './errors';

export interface ModelValues {
 enabled:boolean;
 asr_api_base:string; asr_model:string; asr_revision:string; asr_language_hints:string;
 asr_input_cny_per_million_tokens:string; asr_output_cny_per_million_tokens:string;
 asr_max_input_tokens:number; asr_max_output_tokens:number;
 report_api_base:string; report_model:string; report_revision:string;
 report_input_cny_per_million_tokens:string; report_output_cny_per_million_tokens:string;
 report_max_input_tokens:number; report_max_output_tokens:number;
 monthly_budget_cny:string; price_version:string; price_source:string;
}
export interface ModelSettings {
 revision:string; active_revision:string; values:ModelValues;
 asr_key_configured:boolean; report_key_configured:boolean; can_apply:boolean; provider_connection_tested:boolean;
}
export interface UpdateModelSettings {
 expected_revision:string; values:ModelValues; pricing_confirmed:boolean; reason:string;
 asr_api_key?:string; report_api_key?:string;
}
export function parseModelSettings(raw:unknown):ModelSettings {
 const row=asRecord(raw,'ModelSettings'),source=asRecord(row['values'],'ModelSettings.values');
 const values:Record<string,string|number|boolean>={enabled:asBoolean(source,'enabled','ModelValues')};
 for(const key of ['asr_api_base','asr_model','asr_revision','asr_language_hints','asr_input_cny_per_million_tokens','asr_output_cny_per_million_tokens','report_api_base','report_model','report_revision','report_input_cny_per_million_tokens','report_output_cny_per_million_tokens','monthly_budget_cny','price_version','price_source']) values[key]=asString(source,key,'ModelValues');
 for(const key of ['asr_max_input_tokens','asr_max_output_tokens','report_max_input_tokens','report_max_output_tokens'])values[key]=asInteger(source,key,'ModelValues');
 // A response containing credential fields is a protocol failure, never a form default.
 if(['asr_api_key','report_api_key'].some(key=>key in row||key in source))throw new Error('Model settings response contains credential fields');
 return {revision:asString(row,'revision','ModelSettings'),active_revision:asString(row,'active_revision','ModelSettings'),values:values as unknown as ModelValues,asr_key_configured:asBoolean(row,'asr_key_configured','ModelSettings'),report_key_configured:asBoolean(row,'report_key_configured','ModelSettings'),can_apply:asBoolean(row,'can_apply','ModelSettings'),provider_connection_tested:asBoolean(row,'provider_connection_tested','ModelSettings')};
}
async function read(path:string,body?:UpdateModelSettings):Promise<ModelSettings>{
 const response=await requestJson<unknown>(path,body ? {method:'PUT',body,csrf:true}:{});
 try{return parseModelSettings(response.data);}catch{throw new ApiError({code:'MALFORMED_RESPONSE',status:200,message:'模型配置响应不符合协议'});}
}
export function getModelSettings(){return read('/model-settings');}
export function updateModelSettings(body:UpdateModelSettings){return read('/model-settings',body);}
