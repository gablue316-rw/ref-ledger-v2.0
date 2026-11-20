import {inputDate} from './game-data.js';
export function expenseInput(expense){
 return {date:inputDate(expense.Date),type:expense.Type,value:expense.Type==='Mileage'?String(expense.Mileage):String(expense.Amount).replace(/[$,]/g,''),association:expense.Association,description:expense.Description,taxYear:expense.TaxYear};
}
export function expenseValue(expense){return expense.Type==='Mileage'?`${expense.Mileage} miles`:expense.Amount;}
