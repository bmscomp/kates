package cmd

import (
	"context"
	"fmt"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var securityAuthTestCmd = &cobra.Command{
	Use:     "auth-test",
	Aliases: []string{"auth"},
	Short:   "Probe ACL rules for a specific user to verify least-privilege access",
	Example: "  kates security auth-test --user kafka-ui",
	RunE: func(cmd *cobra.Command, args []string) error {
		if authTestUser == "" {
			return cmdErr("--user flag is required. Example: kates security auth-test --user kafka-ui")
		}

		result, err := apiClient.SecurityAuthTest(context.Background(), authTestUser)
		if err != nil {
			return cmdErr("Auth test failed: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Banner("ACL Auth Test", "User: "+authTestUser)

		checks, _ := result["checks"].([]interface{})
		if len(checks) > 0 {
			rows := make([][]string, 0, len(checks))
			for _, c := range checks {
				check, ok := c.(map[string]interface{})
				if !ok {
					continue
				}
				rows = append(rows, []string{
					statusIcon(fmt.Sprintf("%v", check["status"])),
					fmt.Sprintf("%v", check["name"]),
					fmt.Sprintf("%v", check["detail"]),
				})
			}
			output.Table([]string{"", "Check", "Detail"}, rows)
		}

		aclList, _ := result["acls"].([]interface{})
		if len(aclList) > 0 {
			fmt.Println()
			output.SubHeader(fmt.Sprintf("ACL Rules for User:%s (%d)", authTestUser, len(aclList)))
			rows := make([][]string, 0, len(aclList))
			for _, a := range aclList {
				acl, ok := a.(map[string]interface{})
				if !ok {
					continue
				}
				rows = append(rows, []string{
					fmt.Sprintf("%v", acl["resource"]),
					fmt.Sprintf("%v", acl["name"]),
					fmt.Sprintf("%v", acl["pattern"]),
					fmt.Sprintf("%v", acl["operation"]),
					fmt.Sprintf("%v", acl["permission"]),
				})
			}
			output.Table([]string{"Resource", "Name", "Pattern", "Operation", "Permission"}, rows)
		}

		return nil
	},
}

var securityPentestCmd = &cobra.Command{
	Use:     "pentest",
	Aliases: []string{"pen"},
	Short:   "Run adversarial penetration tests against the cluster",
	Example: `  kates security pentest
  kates security pentest --test metadata-leak
  kates security pentest --test auto-create`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if pentestName == "" {
			pentestName = "all"
		}

		result, err := apiClient.SecurityPentest(context.Background(), pentestName)
		if err != nil {
			return cmdErr("Pentest failed: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Banner("Penetration Test", "Adversarial Security Assessment")

		tests, _ := result["tests"].([]interface{})
		if len(tests) > 0 {
			detailWidth := output.ColumnWidth(56, 30)

			rows := make([][]string, 0, len(tests))
			for _, t := range tests {
				test, ok := t.(map[string]interface{})
				if !ok {
					continue
				}
				res := fmt.Sprintf("%v", test["result"])
				icon := "✓"
				if res == "VULNERABLE" {
					icon = "✗"
				}
				rows = append(rows, []string{
					icon,
					fmt.Sprintf("%v", test["name"]),
					res,
					fmt.Sprintf("%v", test["severity"]),
					truncate(fmt.Sprintf("%v", test["detail"]), detailWidth),
				})
			}
			output.Table([]string{"", "Test", "Result", "Severity", "Detail"}, rows)
		}

		summary, _ := result["summary"].(map[string]interface{})
		if summary != nil {
			fmt.Println()
			output.KeyValue("Total Tests", fmt.Sprintf("%v", summary["total"]))
			output.KeyValue("Protected", output.SuccessStyle.Render(fmt.Sprintf("%v", summary["protected"])))
			output.KeyValue("Vulnerable", output.ErrorStyle.Render(fmt.Sprintf("%v", summary["vulnerable"])))
		}

		return nil
	},
}

var securityACLMapCmd = &cobra.Command{
	Use:     "acl-map",
	Aliases: []string{"coverage", "acl-coverage"},
	Short:   "Show ACL coverage matrix — which users can access which topics",
	Example: "  kates security acl-map",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.SecurityACLMap(context.Background())
		if err != nil {
			return cmdErr("ACL coverage check failed: " + err.Error())
		}
		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		grade := fmt.Sprintf("%v", result["grade"])
		gradeStyled := output.SuccessStyle.Render(grade)
		if grade == "WARN" {
			gradeStyled = output.WarningStyle.Render(grade)
		}
		output.Banner("ACL Coverage Map", fmt.Sprintf("%v Topics  │  %v ACLs  │  %s", result["totalTopics"], result["totalAcls"], gradeStyled))

		opsWidth := output.ColumnWidth(44, 30)

		topics, _ := result["topics"].([]interface{})
		for _, t := range topics {
			topic, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			covered, _ := topic["covered"].(bool)
			icon := "✓"
			if !covered {
				icon = "✗"
			}
			topicName := fmt.Sprintf("%v", topic["topic"])
			users, _ := topic["users"].(map[string]interface{})
			if len(users) == 0 {
				fmt.Printf("  %s  %-35s  %s\n", output.ErrorStyle.Render(icon),
					topicName, output.ErrorStyle.Render("NO ACL RULES"))
			} else {
				fmt.Printf("  %s  %-35s\n", output.SuccessStyle.Render(icon), topicName)
				for user, ops := range users {
					opsStr := fmt.Sprintf("%v", ops)
					fmt.Printf("       %-25s  %s\n", user, truncate(opsStr, opsWidth))
				}
			}
		}

		fmt.Println()
		uncovered := fmt.Sprintf("%v", result["uncoveredTopics"])
		if uncovered != "0" {
			output.KeyValue("Uncovered Topics", output.ErrorStyle.Render(uncovered))
		} else {
			output.KeyValue("Uncovered Topics", output.SuccessStyle.Render("0"))
		}
		return nil
	},
}
