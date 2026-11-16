export function levelOptions(catalog,sport){return catalog.levels.filter(level=>level.active&&(!sport||level.sport===sport));}
export function assignorOptions(catalog,association){return ['Unassigned',...(catalog.associations.find(a=>a.id===association)?.assignors||[]).filter(name=>name!=='Unassigned')];}
