// Renders versionHtml for the instances in $SF_PARITY_IN and writes the HTML list to $SF_PARITY_OUT.
const fs = require("fs");
module.exports = function (ex) {
  const insts = JSON.parse(fs.readFileSync(process.env.SF_PARITY_IN, "utf8"));
  fs.writeFileSync(process.env.SF_PARITY_OUT, JSON.stringify(insts.map((i) => ex.versionHtml(i))));
};
