package schema

type Block struct {
	BlockIndex   int64  `json:"block_index"`
	Hash         string `json:"hash"`
	Timestamp    int64  `json:"timestamp"`
	PreviousHash string `json:"previous_hash"`
	JournalHash  string `json:"journal_hash"`
	Signature    []byte `json:"signature"`
	ContractID   string `json:"contract_id"`
	FunctionName string `json:"function_name"`
	Journal      []byte `json:"journal"`
	ContextID    string `json:"context_id"`
}
