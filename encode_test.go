package ini

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMarshal(t *testing.T) {
	type database struct {
		Server    string
		Port      int
		Encrypted bool
		Size      uint
		Value     float64
	}

	tests := []struct {
		input struct {
			Database database
		}
		want []byte
	}{
		{
			input: struct {
				Database database
			}{
				Database: database{
					Server:    "192.0.2.62",
					Port:      143,
					Encrypted: false,
					Size:      1234,
					Value:     12.34,
				},
			},
			want: []byte("[Database]\nServer=192.0.2.62\nPort=143\nEncrypted=false\nSize=1234\nValue=12.34"),
		},
	}

	for _, test := range tests {
		got, err := Marshal(test.input)
		if err != nil {
			t.Fatal(err)
		}
		if !cmp.Equal(got, test.want) {
			t.Errorf("%q != %q", string(got), string(test.want))
		}
	}
}
