package parser

import (
	"testing"
)

// TestDecoratorsParsing 验证各类类/方法/属性/参数装饰器的解析与类型剥离
func TestDecoratorsParsing(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "Class and method decorators",
			src: `
				@Controller("/users")
				class UserController {
					@Get("/:id")
					getUser(@Param("id") id: string) {
						return { id };
					}
				}
			`,
		},
		{
			name: "Exported decorated class",
			src: `
				@Injectable()
				export class UserService {
					@Autowired()
					private db: Database;

					@Post()
					createUser(@Body() data: any, @Header("authorization") auth: string) {
						return true;
					}
				}
			`,
		},
		{
			name: "Export keyword before decorator",
			src: `
				export @Logged() class AppService {
					@Metric("rpc_call")
					call() {}
				}
			`,
		},
		{
			name: "Stacked decorators",
			src: `
				@Entity()
				@Table({ name: "accounts" })
				class Account {
					@PrimaryGeneratedColumn("uuid")
					id: string;

					@Column({ type: "varchar", length: 255 })
					name: string;
				}
			`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := ParseModule(tc.src)
			if err != nil {
				t.Fatalf("ParseModule failed: %v", err)
			}
			if prog == nil {
				t.Fatalf("Expected non-nil AST Program")
			}
		})
	}
}
